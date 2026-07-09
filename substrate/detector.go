// Package substrate implements the spec-416 on-chain offline detector: it signs and submits
// onlineProfile.report_machine_offline_by_detector(machine_id) to the DBC Substrate chain when
// DDN's verdict engine declares a machine offline.
//
// SAFETY: this path terminates real rentals + zeroes rewards on-chain and is IRREVERSIBLE.
//   - Default mode is SHADOW (compute + log the exact call, never submit).
//   - Submits are SERIALIZED under a mutex with locally-managed nonce (no concurrent-nonce races).
//   - Metadata/runtimeVersion are refreshed when the chain spec_version changes (survives runtime upgrades).
//   - RPC calls are timeout-bounded; on connection error the client reconnects.
//   - Per-machine dedup cooldown prevents duplicate rental-terminating reports.
//   - A live kill-switch (SetKilled) instantly reverts to shadow behaviour.
//   - machine_id = the 64 LOWERCASE-hex ASCII bytes (= on-chain MachineId Vec<u8>), NOT decoded 32 bytes.
//   - The detector key is loaded from config/secret at runtime; NEVER hardcoded/logged.
package substrate

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"DistributedDetectionNode/log"
	mt "DistributedDetectionNode/types"

	gsrpc "github.com/centrifuge/go-substrate-rpc-client/v4"
	"github.com/centrifuge/go-substrate-rpc-client/v4/signature"
	gtypes "github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
	"github.com/sirupsen/logrus"
)

const (
	callMethod    = "OnlineProfile.report_machine_offline_by_detector"
	dialTimeout   = 20 * time.Second
	opTimeout     = 30 * time.Second
	dedupCooldown = 10 * time.Minute // don't re-report the same machine within this window
)

// on-chain MachineId is lowercase hex ASCII; reject anything else so we never target the wrong/no machine.
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Detector is the package-global spec-416 offline reporter (nil until InitDetector succeeds).
var Detector *detector = nil

type detector struct {
	cfg    mt.SubstrateConfig
	kp     signature.KeyringPair
	shadow bool
	killed atomic.Bool // live kill-switch: when set, behaves as shadow

	mu      sync.Mutex // serializes submit: nonce use + kill check + metadata/nonce refresh + reconnect
	api     *gsrpc.SubstrateAPI
	meta    *gtypes.Metadata
	genesis gtypes.Hash
	rv      *gtypes.RuntimeVersion
	nonce   uint32               // next nonce to use (cached; resynced from chain on drift/error)
	recent  map[string]time.Time // dedup: machineID -> last submit time
}

func modeStr(shadow bool) string {
	if shadow {
		return "SHADOW"
	}
	return "LIVE"
}

// withTimeout runs f with a hard deadline (gsrpc RPC calls take no context). A hung call leaks one
// goroutine (bounded, rare) but never blocks the caller — critical for co-location with the DLC DDN.
func withTimeout(d time.Duration, f func() error) error {
	ch := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- fmt.Errorf("panic in rpc op: %v", r)
			}
		}()
		ch <- f()
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		return fmt.Errorf("rpc op timed out after %v", d)
	}
}

// dial connects to the RPC and loads metadata/genesis/runtimeVersion (all timeout-bounded).
func dial(rpc string) (*gsrpc.SubstrateAPI, *gtypes.Metadata, gtypes.Hash, *gtypes.RuntimeVersion, error) {
	var api *gsrpc.SubstrateAPI
	var meta *gtypes.Metadata
	var genesis gtypes.Hash
	var rv *gtypes.RuntimeVersion
	err := withTimeout(dialTimeout, func() error {
		var e error
		if api, e = gsrpc.NewSubstrateAPI(rpc); e != nil {
			return fmt.Errorf("dial %s: %w", rpc, e)
		}
		if meta, e = api.RPC.State.GetMetadataLatest(); e != nil {
			return fmt.Errorf("get metadata: %w", e)
		}
		if genesis, e = api.RPC.Chain.GetBlockHash(0); e != nil {
			return fmt.Errorf("get genesis: %w", e)
		}
		if rv, e = api.RPC.State.GetRuntimeVersionLatest(); e != nil {
			return fmt.Errorf("get runtime version: %w", e)
		}
		return nil
	})
	return api, meta, genesis, rv, err
}

// InitDetector connects to the DBC Substrate RPC and loads the detector wallet.
// No-op (leaves Detector == nil) when cfg.Enabled is false. Timeout-bounded so it can never hang DDN boot.
// Mode must be exactly "live" (case-insensitive) to submit; anything else => SHADOW (fail-safe).
func InitDetector(cfg mt.SubstrateConfig) error {
	if !cfg.Enabled {
		log.Log.Info("substrate offline-detector disabled (Substrate.Enabled=false)")
		return nil
	}
	if strings.TrimSpace(cfg.DetectorSeed) == "" {
		return fmt.Errorf("substrate detector enabled but DetectorSeed empty")
	}
	kp, err := signature.KeyringPairFromSecret(cfg.DetectorSeed, cfg.SS58Prefix)
	if err != nil {
		return fmt.Errorf("load detector key: %w", err)
	}
	api, meta, genesis, rv, err := dial(cfg.Rpc)
	if err != nil {
		return fmt.Errorf("substrate init: %w", err)
	}
	shadow := !strings.EqualFold(strings.TrimSpace(cfg.Mode), "live")

	d := &detector{cfg: cfg, kp: kp, shadow: shadow, api: api, meta: meta, genesis: genesis, rv: rv, recent: make(map[string]time.Time)}
	// nonce only matters for live submission; shadow never submits (and need not require a funded account).
	if !shadow {
		if err := d.resyncNonce(); err != nil {
			return fmt.Errorf("initial nonce sync: %w", err)
		}
	}
	Detector = d
	log.Log.WithFields(logrus.Fields{
		"detector_ss58": kp.Address,
		"detector_pub":  fmt.Sprintf("0x%x", kp.PublicKey),
		"mode":          modeStr(shadow),
		"spec_version":  uint32(rv.SpecVersion),
	}).Warn("substrate offline-detector initialized — register detector_ss58 via set_offline_detectors")
	return nil
}

// SetKilled toggles the live kill-switch. true => stop submitting (behave as shadow).
func (d *detector) SetKilled(v bool) {
	d.killed.Store(v)
	log.Log.WithField("killed", v).Warn("substrate detector kill-switch toggled")
}

// resyncNonce refreshes the cached account nonce from chain (caller holds d.mu, or during init).
func (d *detector) resyncNonce() error {
	key, err := gtypes.CreateStorageKey(d.meta, "System", "Account", d.kp.PublicKey)
	if err != nil {
		return fmt.Errorf("account storage key: %w", err)
	}
	var acc gtypes.AccountInfo
	var ok bool
	if err := withTimeout(opTimeout, func() error {
		var e error
		ok, e = d.api.RPC.State.GetStorageLatest(key, &acc)
		return e
	}); err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	if !ok {
		return fmt.Errorf("detector account %s not found on chain (needs a small DBC balance for fees)", d.kp.Address)
	}
	d.nonce = uint32(acc.Nonce)
	return nil
}

// refreshIfUpgraded re-fetches metadata + runtimeVersion if the chain spec_version changed since we
// cached it (survives runtime upgrades like 413->416). Caller holds d.mu.
func (d *detector) refreshIfUpgraded() error {
	var cur *gtypes.RuntimeVersion
	if err := withTimeout(opTimeout, func() error {
		var e error
		cur, e = d.api.RPC.State.GetRuntimeVersionLatest()
		return e
	}); err != nil {
		return fmt.Errorf("check runtime version: %w", err)
	}
	if uint32(cur.SpecVersion) == uint32(d.rv.SpecVersion) {
		return nil
	}
	log.Log.WithFields(logrus.Fields{"old_spec": uint32(d.rv.SpecVersion), "new_spec": uint32(cur.SpecVersion)}).
		Warn("chain spec_version changed — refreshing metadata")
	var meta *gtypes.Metadata
	if err := withTimeout(opTimeout, func() error {
		var e error
		meta, e = d.api.RPC.State.GetMetadataLatest()
		return e
	}); err != nil {
		return fmt.Errorf("refresh metadata: %w", err)
	}
	d.meta = meta
	d.rv = cur
	return nil
}

// reconnect re-dials the RPC (caller holds d.mu). Used to recover from a dropped connection.
func (d *detector) reconnect() error {
	api, meta, genesis, rv, err := dial(d.cfg.Rpc)
	if err != nil {
		return err
	}
	d.api, d.meta, d.genesis, d.rv = api, meta, genesis, rv
	return d.resyncNonce()
}

// ReportOffline builds report_machine_offline_by_detector(machineID) and submits it, unless in
// shadow/killed mode (then only logs). Serialized + deduped. machineID must be 64 lowercase-hex.
func (d *detector) ReportOffline(machineID string) (string, error) {
	id := strings.TrimSpace(machineID)
	if !hex64.MatchString(id) {
		return "", fmt.Errorf("machine_id not 64-lowercase-hex, refusing to report: %q", machineID)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// dedup: skip if we reported this machine within the cooldown (avoids duplicate rental terminations)
	if last, ok := d.recent[id]; ok && time.Since(last) < dedupCooldown {
		log.Log.WithField("machine", id).Debug("substrate detector: skip duplicate offline report within cooldown")
		return "deduped", nil
	}

	call, err := gtypes.NewCall(d.meta, callMethod, gtypes.Bytes([]byte(id)))
	if err != nil {
		return "", fmt.Errorf("build call %s: %w", callMethod, err)
	}

	// kill check INSIDE the lock so SetKilled has no check-then-submit race window.
	if d.shadow || d.killed.Load() {
		enc, _ := codec.Encode(call)
		log.Log.WithFields(logrus.Fields{
			"machine":  id,
			"call_hex": fmt.Sprintf("0x%x", enc),
			"mode":     "SHADOW",
		}).Warn("[SHADOW] would submit report_machine_offline_by_detector — NOT submitted")
		d.recent[id] = time.Now()
		return "shadow", nil
	}

	// LIVE: refresh metadata if the chain upgraded; rebuild the call against fresh metadata.
	if err := d.refreshIfUpgraded(); err != nil {
		log.Log.WithField("machine", id).Warnf("metadata refresh failed, using cached (may be stale): %v", err)
	} else {
		if call, err = gtypes.NewCall(d.meta, callMethod, gtypes.Bytes([]byte(id))); err != nil {
			return "", fmt.Errorf("rebuild call after refresh: %w", err)
		}
	}

	hash, err := d.signAndSubmit(call)
	if err != nil {
		// one reconnect + resync + retry (covers dropped connection / stale nonce)
		log.Log.WithField("machine", id).Warnf("submit failed (%v) — reconnecting + retrying once", err)
		if rerr := d.reconnect(); rerr != nil {
			return "", fmt.Errorf("submit failed and reconnect failed: %v / %w", err, rerr)
		}
		if call, err = gtypes.NewCall(d.meta, callMethod, gtypes.Bytes([]byte(id))); err != nil {
			return "", fmt.Errorf("rebuild call after reconnect: %w", err)
		}
		if hash, err = d.signAndSubmit(call); err != nil {
			return "", fmt.Errorf("submit failed after reconnect retry: %w", err)
		}
	}
	d.recent[id] = time.Now()
	log.Log.WithFields(logrus.Fields{"machine": id, "tx": hash}).Info("[LIVE] submitted report_machine_offline_by_detector")
	return hash, nil
}

// signAndSubmit signs with the cached nonce and submits; increments the local nonce on success. Caller holds d.mu.
func (d *detector) signAndSubmit(call gtypes.Call) (string, error) {
	ext := gtypes.NewExtrinsic(call)
	o := gtypes.SignatureOptions{
		BlockHash:          d.genesis,
		Era:                gtypes.ExtrinsicEra{IsImmortalEra: true},
		GenesisHash:        d.genesis,
		Nonce:              gtypes.NewUCompactFromUInt(uint64(d.nonce)),
		SpecVersion:        d.rv.SpecVersion,
		Tip:                gtypes.NewUCompactFromUInt(0),
		TransactionVersion: d.rv.TransactionVersion,
	}
	if err := ext.Sign(d.kp, o); err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	var h gtypes.Hash
	if err := withTimeout(opTimeout, func() error {
		var e error
		h, e = d.api.RPC.Author.SubmitExtrinsic(ext)
		return e
	}); err != nil {
		return "", err
	}
	d.nonce++ // advance local nonce only after a successful submit
	return h.Hex(), nil
}
