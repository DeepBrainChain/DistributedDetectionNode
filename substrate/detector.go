// Package substrate implements the spec-416 on-chain offline detector: it signs and submits
// onlineProfile.report_machine_offline_by_detector(machine_id) to the DBC Substrate chain when
// DDN's existing verdict engine declares a machine offline.
//
// SAFETY: this path terminates real rentals + zeroes rewards on-chain and is IRREVERSIBLE.
//   - Default mode is SHADOW (compute + log the exact call, never submit) so the real false-positive
//     rate can be measured against a live spec-416 chain BEFORE any live submission.
//   - A live kill-switch (SetKilled) instantly reverts to shadow behaviour.
//   - machine_id is submitted as the 64 ASCII bytes of the hex string (= on-chain MachineId Vec<u8>,
//     matching bond_machine / MiningBridge), NOT the decoded 32 bytes.
//   - The detector key is loaded from config/secret at runtime; it is NEVER hardcoded or committed.
package substrate

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"DistributedDetectionNode/log"
	mt "DistributedDetectionNode/types"

	gsrpc "github.com/centrifuge/go-substrate-rpc-client/v4"
	"github.com/centrifuge/go-substrate-rpc-client/v4/signature"
	gtypes "github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
	"github.com/sirupsen/logrus"
)

const callMethod = "OnlineProfile.report_machine_offline_by_detector"

var hex64 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// Detector is the package-global spec-416 offline reporter (nil until InitDetector succeeds).
var Detector *detector = nil

type detector struct {
	api     *gsrpc.SubstrateAPI
	meta    *gtypes.Metadata
	genesis gtypes.Hash
	rv      *gtypes.RuntimeVersion
	kp      signature.KeyringPair
	shadow  bool        // true = log-only, never submit
	killed  atomic.Bool // live kill-switch: when set, behaves as shadow
}

func modeStr(shadow bool) string {
	if shadow {
		return "SHADOW"
	}
	return "LIVE"
}

// InitDetector connects to the DBC Substrate RPC and loads the detector wallet.
// No-op (leaves Detector == nil) when cfg.Enabled is false, so the EVM path is unaffected.
func InitDetector(cfg mt.SubstrateConfig) error {
	if !cfg.Enabled {
		log.Log.Info("substrate offline-detector disabled (Chain-substrate.Enabled=false)")
		return nil
	}
	if strings.TrimSpace(cfg.DetectorSeed) == "" {
		return fmt.Errorf("substrate detector enabled but DetectorSeed empty")
	}
	api, err := gsrpc.NewSubstrateAPI(cfg.Rpc)
	if err != nil {
		return fmt.Errorf("substrate dial %s: %w", cfg.Rpc, err)
	}
	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return fmt.Errorf("get metadata: %w", err)
	}
	genesis, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return fmt.Errorf("get genesis hash: %w", err)
	}
	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return fmt.Errorf("get runtime version: %w", err)
	}
	kp, err := signature.KeyringPairFromSecret(cfg.DetectorSeed, cfg.SS58Prefix)
	if err != nil {
		return fmt.Errorf("load detector key: %w", err)
	}
	// shadow unless explicitly set to "live"
	shadow := !strings.EqualFold(strings.TrimSpace(cfg.Mode), "live")

	Detector = &detector{api: api, meta: meta, genesis: genesis, rv: rv, kp: kp, shadow: shadow}
	log.Log.WithFields(logrus.Fields{
		"detector_ss58": kp.Address,
		"detector_pub":  fmt.Sprintf("0x%x", kp.PublicKey),
		"mode":          modeStr(shadow),
		"spec_version":  uint32(rv.SpecVersion),
	}).Warn("substrate offline-detector initialized — register detector_ss58 on-chain via set_offline_detectors")
	return nil
}

// SetKilled toggles the live kill-switch. true => stop submitting (behave as shadow).
func (d *detector) SetKilled(v bool) {
	d.killed.Store(v)
	log.Log.WithField("killed", v).Warn("substrate detector kill-switch toggled")
}

// ReportOffline builds report_machine_offline_by_detector(machineID) and submits it, unless in
// shadow/killed mode (then it only logs the exact call it WOULD submit). machineID must be the
// on-chain 64-hex machine id; it is submitted as its 64 ASCII bytes (Vec<u8>).
func (d *detector) ReportOffline(machineID string) (string, error) {
	id := strings.TrimPrefix(strings.TrimSpace(machineID), "0x")
	if !hex64.MatchString(id) {
		return "", fmt.Errorf("machine_id not 64-hex, refusing to report: %q", machineID)
	}
	call, err := gtypes.NewCall(d.meta, callMethod, gtypes.Bytes([]byte(id)))
	if err != nil {
		return "", fmt.Errorf("build call %s: %w", callMethod, err)
	}

	if d.shadow || d.killed.Load() {
		enc, _ := codec.Encode(call)
		log.Log.WithFields(logrus.Fields{
			"machine":  id,
			"call_hex": fmt.Sprintf("0x%x", enc),
			"mode":     modeStr(true),
		}).Warn("[SHADOW] would submit report_machine_offline_by_detector — NOT submitted")
		return "shadow", nil
	}

	ext := gtypes.NewExtrinsic(call)
	key, err := gtypes.CreateStorageKey(d.meta, "System", "Account", d.kp.PublicKey)
	if err != nil {
		return "", fmt.Errorf("account storage key: %w", err)
	}
	var acc gtypes.AccountInfo
	ok, err := d.api.RPC.State.GetStorageLatest(key, &acc)
	if err != nil {
		return "", fmt.Errorf("get detector account: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("detector account %s not found on chain (needs a small DBC balance for fees)", d.kp.Address)
	}
	o := gtypes.SignatureOptions{
		BlockHash:          d.genesis,
		Era:                gtypes.ExtrinsicEra{IsImmortalEra: true},
		GenesisHash:        d.genesis,
		Nonce:              gtypes.NewUCompactFromUInt(uint64(acc.Nonce)),
		SpecVersion:        d.rv.SpecVersion,
		Tip:                gtypes.NewUCompactFromUInt(0),
		TransactionVersion: d.rv.TransactionVersion,
	}
	if err := ext.Sign(d.kp, o); err != nil {
		return "", fmt.Errorf("sign extrinsic: %w", err)
	}
	h, err := d.api.RPC.Author.SubmitExtrinsic(ext)
	if err != nil {
		return "", fmt.Errorf("submit extrinsic: %w", err)
	}
	log.Log.WithFields(logrus.Fields{
		"machine": id,
		"tx":      h.Hex(),
	}).Info("[LIVE] submitted report_machine_offline_by_detector")
	return h.Hex(), nil
}
