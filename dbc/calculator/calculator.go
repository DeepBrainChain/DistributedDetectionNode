package calculator

import (
	"errors"
	"math"
	"strings"
)

/*
 * https://orion.deeplink.cloud/shortterm
 * https://galaxyrace.deepbrainchain.org/rule
 * https://www.nvidia.cn/geforce/graphics-cards/compare/
 */

type GpuInfo struct {
	Name        string
	CudaCore    int32
	MemoryTotal int32
}

var nvidiaGpuInfoList = []GpuInfo{
	{
		Name:        "2060",
		CudaCore:    1920,
		MemoryTotal: 6,
	},
	{
		Name:        "2060",
		CudaCore:    2176,
		MemoryTotal: 12,
	},
	{
		Name:        "2060 super",
		CudaCore:    2176,
		MemoryTotal: 8,
	},
	{
		Name:        "2070",
		CudaCore:    2304,
		MemoryTotal: 8,
	},
	{
		Name:        "2070 super",
		CudaCore:    2560,
		MemoryTotal: 8,
	},
	{
		Name:        "2080",
		CudaCore:    2944,
		MemoryTotal: 8,
	},
	{
		Name:        "2080 super",
		CudaCore:    3072,
		MemoryTotal: 8,
	},
	{
		Name:        "2080 ti",
		CudaCore:    4352,
		MemoryTotal: 11,
	},
	{
		Name:        "3050",
		CudaCore:    2304,
		MemoryTotal: 6,
	},
	{
		Name:        "3050",
		CudaCore:    2560,
		MemoryTotal: 8,
	},
	{
		Name:        "3060",
		CudaCore:    3584,
		MemoryTotal: 8,
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
	{
		Name:        "5060",
		CudaCore:    3840,
		MemoryTotal: 8,
	},
	{
		Name:        "5060 ti",
		CudaCore:    4608,
		MemoryTotal: 8,
	},
	{
		Name:        "5060 ti",
		CudaCore:    4608,
		MemoryTotal: 16,
	},
	{
		Name:        "5070",
		CudaCore:    6144,
		MemoryTotal: 12,
	},
	{
		Name:        "5070 ti",
		CudaCore:    8960,
		MemoryTotal: 16,
	},
	{
		Name:        "5080",
		CudaCore:    10752,
		MemoryTotal: 16,
	},
	{
		Name:        "5090",
		CudaCore:    21760,
		MemoryTotal: 32,
	},
	{
		Name:        "5090 d",
		CudaCore:    21760,
		MemoryTotal: 32,
	},
}

// gpu model exact match with gpu_memory
func CalculatePointExact(gpuNames []string, gpuMemoryTotals []int32, memoryTotal int32) (float64, error) {
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

// gpu model fuzzy query without gpu_memory
func CalculatePointFuzzy(gpuNames []string, memoryTotal int32) (float64, error) {
	gpuCount := len(gpuNames)
	// cudaCores := make([]int32, 0, len(gpuNames))
	var (
		minCudaCore  int32 = 0
		minGpuMemory int32 = 0
	)
	for _, name := range gpuNames {
		// cudaCores[i] = 0
		// gpuInfo, ok := nvidiaGpuInfoList[name]
		// if !ok {
		// 	return 0.0
		// }
		for _, gpuInfo := range nvidiaGpuInfoList {
			if gpuInfo.Name == name {
				// if strings.EqualFold(gpuInfo.Name, name) {
				// cudaCores[i] = gpuInfo.CudaCore
				if minCudaCore == 0 || gpuInfo.CudaCore < minCudaCore {
					minCudaCore = gpuInfo.CudaCore
					minGpuMemory = gpuInfo.MemoryTotal
				}
				break
			}
		}
	}
	if minCudaCore == 0 {
		return 0.0, errors.New("can not find the number of cuda cores")
	}
	// fmt.Println(minCudaCore)
	// fmt.Println(minGpuMemory)

	point := float64(memoryTotal) / 3.5
	point += float64(gpuCount) * 25
	point += math.Sqrt(float64(minCudaCore)) * math.Sqrt(float64(minGpuMemory)/10) * float64(gpuCount)
	// return point, nil
	ratio := math.Pow(10, 2)
	return math.Round(point*ratio) / ratio, nil
}

func CalculatePointExactFromReport(gpuNames []string, gpuMemoryTotals []int32, memoryTotal int64) (float64, error) {
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
		// matchedGpuNames = append(matchedGpuNames, strings.ToLower(name[fd:]))
		parts := strings.Split(strings.ToLower(name[fd:]), " ")
		filteredParts := []string{}
		for ii, part := range parts {
			if ii == 0 || part == "ti" || part == "super" || part == "d" || part == "oc" || part == "ultra" {
				filteredParts = append(filteredParts, part)
			}
		}
		matchedGpuNames = append(matchedGpuNames, strings.Join(filteredParts, " "))
	}

	// 8192 MB => 8 GB
	for i, mem := range gpuMemoryTotals {
		gpuMemoryTotals[i] = int32(math.Round(float64(mem) / 1024))
	}

	// 17105440768 Bytes => 16 GB
	// memoryTotal = int64(math.Round(float64(memoryTotal) / (1024 * 1024 * 1024)))
	return CalculatePointExact(matchedGpuNames, gpuMemoryTotals, int32(memoryTotal))
}

func CalculatePointFuzzyFromReport(gpuNames []string, memoryTotal int64) (float64, error) {
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
		// matchedGpuNames = append(matchedGpuNames, strings.ToLower(name[fd:]))
		parts := strings.Split(strings.ToLower(name[fd:]), " ")
		filteredParts := []string{}
		for ii, part := range parts {
			if ii == 0 || part == "ti" || part == "super" || part == "d" || part == "oc" || part == "ultra" {
				filteredParts = append(filteredParts, part)
			}
		}
		matchedGpuNames = append(matchedGpuNames, strings.Join(filteredParts, " "))
	}

	// 17105440768 Bytes => 16 GB
	// memoryTotal = int64(math.Round(float64(memoryTotal) / (1024 * 1024 * 1024)))
	return CalculatePointFuzzy(matchedGpuNames, int32(memoryTotal))
}
