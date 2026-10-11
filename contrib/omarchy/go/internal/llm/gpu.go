package llm

import (
	"os/exec"
	"strconv"
	"strings"
)

// GPU is the NVIDIA card's memory in bytes.
type GPU struct {
	Name  string `json:"name"`
	Total int64  `json:"total"`
	Used  int64  `json:"used"`
	Free  int64  `json:"free"`
}

// ParseNvidiaSmi reads "name, total, used, free" (MiB) from
// nvidia-smi --query-gpu=name,memory.total,memory.used,memory.free --format=csv,noheader,nounits
func ParseNvidiaSmi(out string) (GPU, bool) {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	f := strings.Split(line, ",")
	if len(f) != 4 {
		return GPU{}, false
	}
	var n [3]int64
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseInt(strings.TrimSpace(f[i+1]), 10, 64)
		if err != nil {
			return GPU{}, false
		}
		n[i] = v << 20
	}
	return GPU{Name: strings.TrimSpace(f[0]), Total: n[0], Used: n[1], Free: n[2]}, true
}

// QueryGPU asks nvidia-smi; ok is false without an NVIDIA GPU.
func QueryGPU() (GPU, bool) {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name,memory.total,memory.used,memory.free",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return GPU{}, false
	}
	return ParseNvidiaSmi(string(out))
}
