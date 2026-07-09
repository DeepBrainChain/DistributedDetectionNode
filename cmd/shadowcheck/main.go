// shadowcheck: throwaway validation that the substrate detector constructs
// report_machine_offline_by_detector against live spec-416 metadata (shadow mode, never submits).
package main

import (
	"fmt"
	"os"

	"DistributedDetectionNode/substrate"
	"DistributedDetectionNode/types"
)

func main() {
	rpc := "ws://127.0.0.1:9944"
	if len(os.Args) > 1 {
		rpc = os.Args[1]
	}
	cfg := types.SubstrateConfig{
		Enabled:      true,
		Rpc:          rpc,
		DetectorSeed: "//Alice", // dev key, shadow only — never submits
		SS58Prefix:   42,
		Mode:         "shadow",
	}
	if err := substrate.InitDetector(cfg); err != nil {
		fmt.Println("FAIL InitDetector:", err)
		os.Exit(1)
	}
	mid := "8eaf04151687736326c9fea17e25fc5287613693c912909cb226aa4794f26a48"
	h, err := substrate.Detector.ReportOffline(mid)
	if err != nil {
		fmt.Println("FAIL ReportOffline (call did NOT build against 416 metadata):", err)
		os.Exit(1)
	}
	fmt.Println("PASS ReportOffline built call against live 416 metadata + shadow-logged; result =", h)
	if _, err := substrate.Detector.ReportOffline("not-a-hex-id"); err != nil {
		fmt.Println("PASS invalid machine_id correctly rejected:", err)
	} else {
		fmt.Println("FAIL invalid machine_id NOT rejected")
		os.Exit(1)
	}
}
