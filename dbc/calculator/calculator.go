package calculator

import (
	"errors"
	"math"
	"strings"
)

/*
 * https://orion.deeplink.cloud/shortterm
 * https://galaxyrace.deepbrainchain.org/rule
 */

type GpuInfo struct {
	Name        string
	CudaCore    int32
	MemoryTotal int32
}

var nvidiaGpuInfoList = []GpuInfo{
	{
		Name:        "2080 ti",
		CudaCore:    4352,
		MemoryTotal: 11,
	},
	{
		Name:        "3060",
		CudaCore:    3584,
		MemoryTotal: 12,
	},
	{
		Name:        "3060 ti",
		CudaCore:    4864,
		MemoryTotal: 8,
	},
	{
		Name:        "3070",
		CudaCore:    5888,
		MemoryTotal: 8,
	},
	{
		Name:        "3070 ti",
		CudaCore:    6144,
		MemoryTotal: 8,
	},
	{
		Name:        "3080",
		CudaCore:    8704,
		MemoryTotal: 10,
	},
	{
		Name:        "3080",
		CudaCore:    8960,
		MemoryTotal: 12,
	},
	{
		Name:        "3080 ti",
		CudaCore:    10240,
		MemoryTotal: 12,
	},
	{
		Name:        "3090",
		CudaCore:    10496,
		MemoryTotal: 24,
	},
	{
		Name:        "3090 ti",
		CudaCore:    10752,
		MemoryTotal: 24,
	},
	{
		Name:        "4060",
		CudaCore:    3072,
		MemoryTotal: 8,
	},
	{
		Name:        "4060 ti",
		CudaCore:    4352,
		MemoryTotal: 8,
	},
	{
		Name:        "4060 ti",
		CudaCore:    4352,
		MemoryTotal: 16,
	},
	{
		Name:        "4070",
		CudaCore:    5888,
		MemoryTotal: 12,
	},
	{
		Name:        "4070 super",
		CudaCore:    7168,
		MemoryTotal: 12,
	},
	{
		Name:        "4070 ti",
		CudaCore:    7680,
		MemoryTotal: 12,
	},
	{
		Name:        "4070 ti super",
		CudaCore:    8448,
		MemoryTotal: 16,
	},
	{
		Name:        "4080",
		CudaCore:    9728,
		MemoryTotal: 16,
	},
	{
		Name:        "4080 super",
		CudaCore:    10240,
		MemoryTotal: 16,
	},
	{
		Name:        "4080 ti",
		CudaCore:    14080,
		MemoryTotal: 20,
	},
	{
		Name:        "4090",
		CudaCore:    16384,
		MemoryTotal: 24,
	},
	{
		Name:        "4090 ti",
		CudaCore:    18176,
		MemoryTotal: 24,
	},
	{
		Name:        "4090 d",
		CudaCore:    14592,
		MemoryTotal: 24,
	},
}

func CalculatePoint(gpuNames []string, gpuMemoryTotals []int32, memoryTotal int32) (float64, error) {
	if len(gpuNames) != len(gpuMemoryTotals) {
		return 0.0, errors.New("gpu name and memory list are inconsistent")
	}
	gpuCount := len(gpuNames)
	// cudaCores := make([]int32, 0, len(gpuNames))
	var (
		minCudaCore int32 = 0
		minIndex    int   = 0
	)
	for i, name := range gpuNames {
		// cudaCores[i] = 0
		// gpuInfo, ok := nvidiaGpuInfoList[name]
		// if !ok {
		// 	return 0.0
		// }
		for _, gpuInfo := range nvidiaGpuInfoList {
			if gpuInfo.Name == name && gpuInfo.MemoryTotal == gpuMemoryTotals[i] {
				// if strings.EqualFold(gpuInfo.Name, name) && gpuInfo.MemoryTotal == gpuMemoryTotals[i] {
				// cudaCores[i] = gpuInfo.CudaCore
				if minCudaCore == 0 || gpuInfo.CudaCore < minCudaCore {
					minCudaCore = gpuInfo.CudaCore
					minIndex = i
				}
				break
			}
		}
	}
	if minCudaCore == 0 {
		return 0.0, errors.New("can not find the number of cuda cores")
	}
	// fmt.Println(minCudaCore)
	// fmt.Println(minIndex)

	point := float64(memoryTotal) / 3.5
	point += float64(gpuCount) * 25
	point += math.Sqrt(float64(minCudaCore)) * math.Sqrt(float64(gpuMemoryTotals[minIndex])/10) * float64(gpuCount)
	// return point, nil
	ratio := math.Pow(10, 2)
	return math.Round(point*ratio) / ratio, nil
}

func CalculatePointFromReport(gpuNames []string, gpuMemoryTotals []int32, memoryTotal int64) (float64, error) {
	findFirstDigit := func(s string) int {
		for i, char := range s {
			if char >= '0' && char <= '9' {
				return i
			}
		}
		return -1
	}

	// "NVIDIA GeForce RTX 4060 Ti" => "4060 ti"
	matchedGpuNames := make([]string, 0)
	for _, name := range gpuNames {
		fd := findFirstDigit(name)
		if fd == -1 {
			continue
		}
		matchedGpuNames = append(matchedGpuNames, strings.ToLower(name[fd:]))
	}

	// 8192 MB => 8 GB
	for i, mem := range gpuMemoryTotals {
		gpuMemoryTotals[i] = mem / 1024
	}

	// 17105440768 Bytes => 16 GB
	memoryTotal = int64(math.Round(float64(memoryTotal) / (1024 * 1024 * 1024)))
	return CalculatePoint(matchedGpuNames, gpuMemoryTotals, int32(memoryTotal))
}
