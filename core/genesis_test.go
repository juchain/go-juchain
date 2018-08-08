// Copyright 2018 The go-juchain Authors
// This file is part of the go-juchain library.
//
// The go-juchain library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-juchain library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-juchain library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"math/big"
	"reflect"
	"testing"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/core/store"
	"github.com/juchain/go-juchain/config"
	"github.com/juchain/go-juchain/vm/solc"
	"github.com/juchain/go-juchain/consensus"
)

var (
	MainnetGenesisHash = common.HexToHash("0xaac5a02784773dad8b576cd7434cca5a6865566a5ca0ae0dc69a4d64a7597995") // Mainnet genesis hash to enforce below configs on
	TestnetGenesisHash = common.HexToHash("0x10236961b6d4c17462d88af07224782bc626066f750dd7ed09581fa67a4277fc") // Testnet genesis hash to enforce below configs on
)

func TestDefaultGenesisBlock(t *testing.T) {
	block := DefaultGenesisBlock().ToBlock(nil)
	fmt.Println("MainnetGenesisHash: " + block.Hash().String())
	if block.Hash() != MainnetGenesisHash {
		t.Errorf("wrong mainnet genesis hash, got %v, want %v", block.Hash(), MainnetGenesisHash)
	}
	block = DefaultTestnetGenesisBlock().ToBlock(nil)
	fmt.Println("TestnetGenesisHash: " + block.Hash().String())
	if block.Hash() != TestnetGenesisHash {
		t.Errorf("wrong testnet genesis hash, got %v, want %v", block.Hash(), TestnetGenesisHash)
	}
}

func TestSetupGenesis(t *testing.T) {
	var (
		customghash = common.HexToHash("0x3f26eafc08da45cf4f1da01c60d50e153af2a036a562fcf872a8a58de0658f36")
		customg     = Genesis{
			Config: &config.ChainConfig{},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1000000000000000000), Storage: map[common.Hash]common.Hash{{1}: {1}}},
			},
		}
		oldcustomg = customg
	)
	oldcustomg.Config = &config.ChainConfig{}
	tests := []struct {
		name       string
		fn         func(store.Database) (*config.ChainConfig, common.Hash, error)
		wantConfig *config.ChainConfig
		wantHash   common.Hash
		wantErr    error
	}{
		{
			name: "genesis without ChainConfig",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, new(Genesis))
			},
			wantErr:    errGenesisNoConfig,
			wantConfig: config.MainnetChainConfig,
		},
		{
			name: "no block in DB, genesis == nil",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   MainnetGenesisHash,
			wantConfig: config.MainnetChainConfig,
		},
		{
			name: "mainnet block in DB, genesis == nil",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				DefaultGenesisBlock().MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   MainnetGenesisHash,
			wantConfig: config.MainnetChainConfig,
		},
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   customghash,
			wantConfig: config.MainnetChainConfig,
		},
		{
			name: "custom block in DB, genesis == testnet",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, DefaultTestnetGenesisBlock())
			},
			wantErr:    &GenesisMismatchError{Stored: customghash, New: TestnetGenesisHash},
			wantHash:   TestnetGenesisHash,
			wantConfig: config.TestnetChainConfig,
		},
		{
			name: "compatible config in DB",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				oldcustomg.MustCommit(db)
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "incompatible config in DB",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				// Commit the 'old' genesis block with Homestead transition at #2.
				// Advance to block #4, past the homestead transition block of customg.
				genesis := oldcustomg.MustCommit(db)
				engine  := consensus.CreateFakeEngine()
				bc, _ := NewBlockChain(db, nil, oldcustomg.Config, engine, vm.Config{})
				defer bc.Stop()
				blocks, _ := GenerateChain(oldcustomg.Config, genesis, engine, db, 4, nil)
				bc.InsertChain(blocks)
				bc.CurrentBlock()
				// This should return a compatibility error.
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
			wantErr: &config.ConfigCompatError{
				What:         "Homestead fork block",
				StoredConfig: big.NewInt(2),
				NewConfig:    big.NewInt(3),
				RewindTo:     1,
			},
		},
	}

	for _, test := range tests {
		db, _ := store.NewMemDatabase()
		config, hash, err := test.fn(db)
		// Check the return values.
		if err != nil && !reflect.DeepEqual(err, test.wantErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(err), spew.NewFormatter(test.wantErr))
		}
		if !reflect.DeepEqual(config, test.wantConfig) {
			t.Errorf("%s:\nreturned %v\nwant     %v", test.name, config, test.wantConfig)
		}
		if hash != test.wantHash {
			t.Errorf("%s: returned hash %s, want %s", test.name, hash.Hex(), test.wantHash.Hex())
		} else if err == nil {
			// Check database content.
			stored := GetBlock(db, test.wantHash, 0)
			if stored.Hash() != test.wantHash {
				t.Errorf("%s: block in DB has hash %s, want %s", test.name, stored.Hash(), test.wantHash)
			}
		}
	}
}
