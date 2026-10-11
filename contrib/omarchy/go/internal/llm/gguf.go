package llm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// GGUFInfo is what the panel shows from a model file's header.
type GGUFInfo struct {
	Arch  string
	Quant string
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
	var info GGUFInfo
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
	skip := func(t uint32) error {
		if sz, ok := gSize[t]; ok {
			_, err := r.Discard(int(sz))
			return err
		}
		if t == gString {
			_, err := readStr()
			return err
		}
		return fmt.Errorf("type %d", t)
	}
	if hdr.KVs > 1<<16 {
		return info, errors.New("implausible GGUF header")
	}
	for i := uint64(0); i < hdr.KVs && (info.Arch == "" || info.Quant == ""); i++ {
		key, err := readStr()
		if err != nil {
			return info, err
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
		case key == "general.file_type" && t == gU32:
			var v uint32
			if err := binary.Read(r, le, &v); err != nil {
				return info, err
			}
			info.Quant = fileTypes[v]
		case t == gArray:
			var et uint32
			var n uint64
			if err := binary.Read(r, le, &et); err != nil {
				return info, err
			}
			if err := binary.Read(r, le, &n); err != nil {
				return info, err
			}
			if sz, ok := gSize[et]; ok {
				if _, err := r.Discard(int(sz * int64(n))); err != nil {
					return info, err
				}
				continue
			}
			for j := uint64(0); j < n; j++ {
				if err := skip(et); err != nil {
					return info, err
				}
			}
		default:
			if err := skip(t); err != nil {
				return info, err
			}
		}
	}
	return info, nil
}
