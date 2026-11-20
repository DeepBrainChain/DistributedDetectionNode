package substrate

import (
	"fmt"
	"strings"

	"github.com/centrifuge/go-substrate-rpc-client/v4/signature"
	gtypes "github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types/codec"
)

// dbcSignedExtensions is the DBC runtime's signed-extension list (spec 417 and 418).
// gsrpc v4's Extrinsic.Sign only knows Era/Nonce/Tip, but DBC uses
// ChargeAssetTxPayment, whose extra is (tip, asset_id: Option<MultiLocation>).
// The asset_id byte gsrpc leaves out makes every extrinsic undecodable, so the
// runtime rejects it ("wasm unreachable" in validate_transaction). We encode the
// extrinsic ourselves and refuse to sign if the chain's list ever changes.
var dbcSignedExtensions = []string{
	"CheckNonZeroSender",
	"CheckSpecVersion",
	"CheckTxVersion",
	"CheckGenesis",
	"CheckMortality",
	"CheckNonce",
	"CheckWeight",
	"ChargeAssetTxPayment",
}

// checkSignedExtensions fails when the chain metadata's signed extensions differ
// from dbcSignedExtensions (so a runtime change can't silently produce bad txs).
func checkSignedExtensions(meta *gtypes.Metadata) error {
	if meta == nil || meta.Version != 14 {
		return fmt.Errorf("signed extensions: metadata v14 required")
	}
	exts := meta.AsMetadataV14.Extrinsic.SignedExtensions
	got := make([]string, 0, len(exts))
	for _, e := range exts {
		got = append(got, string(e.Identifier))
	}
	if strings.Join(got, ",") != strings.Join(dbcSignedExtensions, ",") {
		return fmt.Errorf("signed extensions changed: chain has [%s], detector supports [%s]",
			strings.Join(got, ","), strings.Join(dbcSignedExtensions, ","))
	}
	return nil
}

// encodeSignedExtrinsic builds a v4 signed extrinsic for the DBC signed-extension
// list and returns it hex-encoded (with its length prefix), ready for
// author_submitExtrinsic.
func encodeSignedExtrinsic(kp signature.KeyringPair, call gtypes.Call, o gtypes.SignatureOptions) (string, error) {
	enc := func(v interface{}) ([]byte, error) { return codec.Encode(v) }

	callB, err := enc(call)
	if err != nil {
		return "", fmt.Errorf("encode call: %w", err)
	}
	eraB, err := enc(o.Era)
	if err != nil {
		return "", fmt.Errorf("encode era: %w", err)
	}
	nonceB, err := enc(o.Nonce)
	if err != nil {
		return "", fmt.Errorf("encode nonce: %w", err)
	}
	tipB, err := enc(o.Tip)
	if err != nil {
		return "", fmt.Errorf("encode tip: %w", err)
	}
	specB, err := enc(o.SpecVersion)
	if err != nil {
		return "", fmt.Errorf("encode spec version: %w", err)
	}
	txvB, err := enc(o.TransactionVersion)
	if err != nil {
		return "", fmt.Errorf("encode tx version: %w", err)
	}

	// extra: CheckMortality(era) CheckNonce(nonce) ChargeAssetTxPayment(tip, asset_id=None)
	extra := concatBytes(eraB, nonceB, tipB, []byte{0x00})
	// additional signed: spec, tx version, genesis, era block hash
	payload := concatBytes(callB, extra, specB, txvB, o.GenesisHash[:], o.BlockHash[:])

	sig, err := signature.Sign(payload, kp.URI) // hashes payloads > 256 bytes
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	if len(kp.PublicKey) != 32 || len(sig) != 64 {
		return "", fmt.Errorf("unexpected key/signature length %d/%d", len(kp.PublicKey), len(sig))
	}

	body := concatBytes(
		[]byte{0x84},       // signed, extrinsic format v4
		[]byte{0x00},       // MultiAddress::Id
		kp.PublicKey,       // AccountId32
		[]byte{0x01},       // MultiSignature::Sr25519
		sig,                // 64 bytes
		extra,              // signed extensions' extra data
		callB,              // the call
	)
	lenB, err := enc(gtypes.NewUCompactFromUInt(uint64(len(body))))
	if err != nil {
		return "", fmt.Errorf("encode length: %w", err)
	}
	return codec.HexEncodeToString(concatBytes(lenB, body)), nil
}

func concatBytes(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
