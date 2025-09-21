package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

type SeedPack struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Seeds   []uint64 `json:"seeds"`
	hash    string
}

type SeedPackMeta struct {
	Name    string
	Version string
	Count   int
	SHA256  string
}

func LoadSeedPack(path string) (*SeedPack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sp SeedPack
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, err
	}
	if len(sp.Seeds) == 0 {
		return nil, fmt.Errorf("seedpack has no seeds")
	}
	hash := sha256.Sum256(data)
	sp.hash = hex.EncodeToString(hash[:])
	return &sp, nil
}

func (sp *SeedPack) Metadata() SeedPackMeta {
	if sp == nil {
		return SeedPackMeta{}
	}
	return SeedPackMeta{
		Name:    sp.Name,
		Version: sp.Version,
		Count:   len(sp.Seeds),
		SHA256:  sp.hash,
	}
}

func (sp *SeedPack) SeedAt(idx int) (uint64, error) {
	if sp == nil {
		return 0, fmt.Errorf("seedpack nil")
	}
	if idx < 0 || idx >= len(sp.Seeds) {
		return 0, fmt.Errorf("seed index %d out of range", idx)
	}
	return sp.Seeds[idx], nil
}
