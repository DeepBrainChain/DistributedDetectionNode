package calculator

import (
	"log"
	"math"
	"strings"
	"testing"
)

// go test -v -timeout 30s -count=1 -run TestCalcPoint DistributedDetectionNode/dbc/calculator
func TestCalcPoint(t *testing.T) {
	gpuNames := []string{"NVIDIA GeForce RTX 4060 Ti"}
	// gpuMemoryTotals := []int32{8192}
	gpuMemoryTotals := []int32{16384}
	// gpuNames := []string{
	// 	"NVIDIA GeForce RTX 4060 Ti",
	// 	"NVIDIA GeForce RTX 4060 Ti",
	// 	"NVIDIA GeForce RTX 4060 Ti",
	// 	"NVIDIA GeForce RTX 4060 Ti",
	// }
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

	calcPoint, err := CalculatePoint(gpuNames, gpuMemoryTotals, int32(memoryTotal))
	log.Printf("%v %v %v %v", gpuNames, gpuMemoryTotals, calcPoint, err)
}

func TestCalcPoint2(t *testing.T) {
	gpuNames := []string{"NVIDIA GeForce RTX 4060 Ti"}
	// gpuMemoryTotals := []int32{8192}
	gpuMemoryTotals := []int32{16384}
	memoryTotal := int64(17105440768)
	// memoryTotal := int64(34256556032)
	memoryTotal = int64(math.Round(float64(memoryTotal) / (1024 * 1024 * 1024)))

	calcPoint, err := CalculatePointFromReport(gpuNames, gpuMemoryTotals, memoryTotal)
	log.Printf("%v %v %v %v", gpuNames, gpuMemoryTotals, calcPoint, err)

	gpuNames = []string{
		"NVIDIA GeForce RTX 4080",
		"NVIDIA GeForce RTX 4060 Ti",
		"NVIDIA GeForce RTX 4060 Ti",
		"NVIDIA GeForce RTX 4060 Ti",
	}
	gpuMemoryTotals = []int32{16384, 8192, 8192, 8192}
	memoryTotal = int64(128) // int64(137438953472)

	calcPoint, err = CalculatePointFromReport(gpuNames, gpuMemoryTotals, memoryTotal)
	log.Printf("%v %v %v %v", gpuNames, gpuMemoryTotals, calcPoint, err)
}

func TestCalcPoint3(t *testing.T) {
	gpuNames := []string{"NVIDIA GeForce RTX 4070"}
	// gpuMemoryTotals := []int32{8192}
	gpuMemoryTotals := []int32{12282}
	memoryTotal := int64(32)

	calcPoint, err := CalculatePointFromReport(gpuNames, gpuMemoryTotals, memoryTotal)
	log.Printf("%v %v %v %v", gpuNames, gpuMemoryTotals, calcPoint, err)
}
