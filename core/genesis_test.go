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

	"github.com/davecgh/go-spew/spew"
	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/core/store"
	"github.com/juchain/go-juchain/config"
	"github.com/juchain/go-juchain/vm/solc"
	"github.com/juchain/go-juchain/consensus"
	"github.com/juchain/go-juchain/core/types"
	"github.com/juchain/go-juchain/core/state"
	"github.com/juchain/go-juchain/rpc"
	"fmt"
)

func CreateFakeEngine() *FakeEngine {
	return &FakeEngine{};
}

type FakeEngine struct {
}

func (f *FakeEngine) Author(header *types.Header) (common.Address, error) {
	return common.Address{}, nil;
}

func (f *FakeEngine) VerifyHeader(chain consensus.ChainReader, header *types.Header, seal bool) error {
	return nil;
}

func (f *FakeEngine) VerifyHeaders(chain consensus.ChainReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, _ := range headers {
			err := returnEmpty();
			fmt.Println(i);
			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

func returnEmpty() error {
	return nil;
}

func (f *FakeEngine) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	return nil;
}

func (f *FakeEngine) VerifySeal(chain consensus.ChainReader, header *types.Header) error {
	return nil;
}

func (f *FakeEngine)  Prepare(chain consensus.ChainReader, header *types.Header) error {
	return nil;
}

func (f *FakeEngine) Finalize(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
	uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	return types.NewBlock(header, txs, uncles, receipts), nil
}

func (f *FakeEngine) Seal(chain consensus.ChainReader, block *types.Block, stop <-chan struct{}) (*types.Block, error) {
	return block, nil;
}

func (f *FakeEngine) CalcDifficulty(chain consensus.ChainReader, time uint64, parent *types.Header) *big.Int {
	return big.NewInt(123456);
}

func (f *FakeEngine) APIs(chain consensus.ChainReader) []rpc.API {
	return []rpc.API{{
		Namespace: "fake",
		Version:   "1.0",
		Service:   &FakeAPI{chain: chain},
		Public:    false,
	}}
}

// PublicDebugAPI is the collection of Ethereum APIs exposed over the public
// debugging endpoint.
type FakeAPI struct {
	chain  consensus.ChainReader
}

func (dpos *FakeAPI) Test() string {
	return "test";
}


func TestDefaultGenesisBlock(t *testing.T) {
	block := DefaultGenesisBlock().ToBlock(nil)
	//fmt.Println(block.Hash().String())
	if block.Hash() != config.MainnetGenesisHash {
		t.Errorf("wrong mainnet genesis hash, got %v, want %v", block.Hash(), config.MainnetGenesisHash)
	}
	block = DefaultTestnetGenesisBlock().ToBlock(nil)
	//fmt.Println(block.Hash().String())
	if block.Hash() != config.TestnetGenesisHash {
		t.Errorf("wrong testnet genesis hash, got %v, want %v", block.Hash(), config.TestnetGenesisHash)
	}
}

func TestSetupGenesis(t *testing.T) {
	var (
		customghash = common.HexToHash("0x89c99d90b79719238d2645c7642f2c9295246e80775b38cfd162b696817fbd50")
		customg     = Genesis{
			Config: &config.ChainConfig{},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
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
			wantConfig: config.AllEthashProtocolChanges,
		},
		{
			name: "no block in DB, genesis == nil",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   config.MainnetGenesisHash,
			wantConfig: config.MainnetChainConfig,
		},
		{
			name: "mainnet block in DB, genesis == nil",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				DefaultGenesisBlock().MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   config.MainnetGenesisHash,
			wantConfig: config.MainnetChainConfig,
		},
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "custom block in DB, genesis == testnet",
			fn: func(db store.Database) (*config.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, DefaultTestnetGenesisBlock())
			},
			wantErr:    &GenesisMismatchError{Stored: customghash, New: config.TestnetGenesisHash},
			wantHash:   config.TestnetGenesisHash,
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
				engine := CreateFakeEngine()
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
		if !reflect.DeepEqual(err, test.wantErr) {
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
