package dbc

import (
	"log"
	"math"
	"strings"
	"testing"
)

// go test -v -timeout 30s -count=1 -run TestCalcPoint DistributedDetectionNode/dbc
func TestCalcPoint(t *testing.T) {
	gpuNames := []string{"NVIDIA GeForce RTX 4060 Ti"}
	// gpuMemoryTotals := []int32{8192}
	gpuMemoryTotals := []int32{16384}
	// gpuNames := []string{"NVIDIA GeForce RTX 4060 Ti", "NVIDIA GeForce RTX 4060 Ti", "NVIDIA GeForce RTX 4060 Ti", "NVIDIA GeForce RTX 4060 Ti"}
	// gpuMemoryTotals := []int32{8192, 8192, 8192, 8192}
	memoryTotal := 17105440768
	// memoryTotal := 34256556032

	findFirstDigit := func(s string) int {
		for i, char := range s {
			if char >= '0' && char <= '9' {
				return i
			}
		}
		return -1
	}

	for i, name := range gpuNames {
		fd := findFirstDigit(name)
		if fd == -1 {
			log.Fatalf("invalid gpu type: %v", name)
		}
		gpuNames[i] = strings.ToLower(name[fd:])
	}
	log.Printf("match gpu type: %v", gpuNames)

	for i, mem := range gpuMemoryTotals {
		gpuMemoryTotals[i] = mem / 1024
	}
	log.Printf("format gpu memory unit to GB: %v", gpuMemoryTotals)

	memoryTotal = int(math.Round(float64(memoryTotal) / (1024 * 1024 * 1024)))
	log.Printf("physical memory total: %vGB", memoryTotal)

	log.Printf("%v %v %v", gpuNames, gpuMemoryTotals, CalculatePoint(gpuNames, gpuMemoryTotals, int32(memoryTotal)))
}

func TestCalcPoint2(t *testing.T) {
	gpuNames := []string{"NVIDIA GeForce RTX 4060 Ti"}
	// gpuMemoryTotals := []int32{8192}
	gpuMemoryTotals := []int32{16384}
	memoryTotal := 17105440768
	// memoryTotal := 34256556032

	log.Printf("%v %v %v", gpuNames, gpuMemoryTotals, CalculatePointFromReport(gpuNames, gpuMemoryTotals, int64(memoryTotal)))

	gpuNames = []string{"NVIDIA GeForce RTX 4080", "NVIDIA GeForce RTX 4060 Ti", "NVIDIA GeForce RTX 4060 Ti", "NVIDIA GeForce RTX 4060 Ti"}
	gpuMemoryTotals = []int32{16384, 8192, 8192, 8192}
	memoryTotal = 137438953472

	log.Printf("%v %v %v", gpuNames, gpuMemoryTotals, CalculatePointFromReport(gpuNames, gpuMemoryTotals, int64(memoryTotal)))
}
