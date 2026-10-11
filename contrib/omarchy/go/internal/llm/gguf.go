package llm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// GGUFInfo is what the panel and the auto-tuner read from a model's header.
type GGUFInfo struct {
	Arch   string
	Quant  string
	Ints   map[string]int64   // numeric keys of the architecture ("block_count" without the "<arch>." prefix)
	Arrays map[string][]int64 // per-layer numeric arrays (head_count_kv, sliding_window_pattern…)
}

// Int returns an architecture key, or the max of its per-layer array, or def.
func (g GGUFInfo) Int(key string, def int64) int64 {
	if v, ok := g.Ints[key]; ok {
		return v
	}
	if a, ok := g.Arrays[key]; ok && len(a) > 0 {
		m := a[0]
		for _, x := range a {
			m = max(m, x)
		}
		return m
	}
	return def
}

var fileTypes = map[uint32]string{0: "F32", 1: "F16", 2: "Q4_0", 3: "Q4_1", 7: "Q8_0", 8: "Q5_0", 9: "Q5_1",
	10: "Q2_K", 11: "Q3_K_S", 12: "Q3_K_M", 13: "Q3_K_L", 14: "Q4_K_S", 15: "Q4_K_M", 16: "Q5_K_S", 17: "Q5_K_M",
	18: "Q6_K", 19: "IQ2_XXS", 20: "IQ2_XS", 21: "Q2_K_S", 22: "IQ3_XS", 23: "IQ3_XXS", 24: "IQ1_S", 25: "IQ4_NL",
	26: "IQ3_S", 27: "IQ3_M", 28: "IQ2_S", 29: "IQ2_M", 30: "IQ4_XS", 31: "IQ1_M", 32: "BF16", 36: "TQ1_0",
	37: "TQ2_0", 38: "MXFP4_MOE"}

// gguf value types
const (
	gU8 = iota
	gI8
	gU16
	gI16
	gU32
	gI32
	gF32
	gBool
	gString
	gArray
	gU64
	gI64
	gF64
)

var gSize = map[uint32]int64{gU8: 1, gI8: 1, gU16: 2, gI16: 2, gU32: 4, gI32: 4, gF32: 4, gBool: 1, gU64: 8, gI64: 8, gF64: 8}

// ReadGGUFInfo reads general.architecture and general.file_type, stopping as
// soon as both are known (they come before the big tokenizer arrays).
func ReadGGUFInfo(path string) (GGUFInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return GGUFInfo{}, err
	}
	defer f.Close()
	return parseGGUF(bufio.NewReaderSize(f, 1<<16))
}

func parseGGUF(r *bufio.Reader) (GGUFInfo, error) {
	info := GGUFInfo{Ints: map[string]int64{}, Arrays: map[string][]int64{}}
	le := binary.LittleEndian
	var hdr struct {
		Magic   [4]byte
		Version uint32
		Tensors uint64
		KVs     uint64
	}
	if err := binary.Read(r, le, &hdr); err != nil {
		return info, err
	}
	if string(hdr.Magic[:]) != "GGUF" || hdr.Version < 2 {
		return info, errors.New("not a GGUF v2+ file")
	}
	if hdr.KVs > 1<<16 {
		return info, errors.New("implausible GGUF header")
	}
	readStr := func() (string, error) {
		var n uint64
		if err := binary.Read(r, le, &n); err != nil {
			return "", err
		}
		if n > 1<<20 {
			return "", fmt.Errorf("string of %d bytes", n)
		}
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		return string(b), err
	}
	readNum := func(t uint32) (int64, bool, error) {
		switch t {
		case gU8, gBool:
			b, err := r.ReadByte()
			return int64(b), true, err
		case gI8:
			b, err := r.ReadByte()
			return int64(int8(b)), true, err
		case gU16, gI16:
			var v uint16
			err := binary.Read(r, le, &v)
			if t == gI16 {
				return int64(int16(v)), true, err
			}
			return int64(v), true, err
		case gU32, gI32:
			var v uint32
			err := binary.Read(r, le, &v)
			if t == gI32 {
				return int64(int32(v)), true, err
			}
			return int64(v), true, err
		case gU64, gI64:
			var v uint64
			err := binary.Read(r, le, &v)
			return int64(v), true, err
		case gF32:
			_, err := r.Discard(4)
			return 0, false, err
		case gF64:
			_, err := r.Discard(8)
			return 0, false, err
		case gString:
			_, err := readStr()
			return 0, false, err
		}
		return 0, false, fmt.Errorf("type %d", t)
	}
	prefix := ""
	for i := uint64(0); i < hdr.KVs; i++ {
		key, err := readStr()
		if err != nil {
			return info, err
		}
		if info.Arch != "" && len(key) > 10 && key[:10] == "tokenizer." {
			break // everything the tuner needs comes before the (huge) tokenizer
		}
		var t uint32
		if err := binary.Read(r, le, &t); err != nil {
			return info, err
		}
		switch {
		case key == "general.architecture" && t == gString:
			if info.Arch, err = readStr(); err != nil {
				return info, err
			}
			prefix = info.Arch + "."
		case t == gArray:
			var et uint32
			var n uint64
			if err := binary.Read(r, le, &et); err != nil {
				return info, err
			}
			if err := binary.Read(r, le, &n); err != nil {
				return info, err
			}
			keep := prefix != "" && len(key) > len(prefix) && key[:len(prefix)] == prefix && n <= 4096 && et != gString
			var vals []int64
			for j := uint64(0); j < n; j++ {
				v, num, err := readNum(et)
				if err != nil {
					return info, err
				}
				if keep && num {
					vals = append(vals, v)
				}
			}
			if keep && len(vals) > 0 {
				info.Arrays[key[len(prefix):]] = vals
			}
		default:
			v, num, err := readNum(t)
			if err != nil {
				return info, err
			}
			if key == "general.file_type" {
				info.Quant = fileTypes[uint32(v)]
			} else if num && prefix != "" && len(key) > len(prefix) && key[:len(prefix)] == prefix {
				info.Ints[key[len(prefix):]] = v
			}
		}
	}
	return info, nil
}
