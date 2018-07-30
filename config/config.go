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

package config

import (
	"fmt"
	"math/big"

	"github.com/juchain/go-juchain/common"
	"time"
)

var (
	// All assigned DApps to this node.
	DAppAddresses *DAppAddress;

	// MainnetChainConfig is the chain parameters to run a node on the main network.
	MainnetChainConfig = &ChainConfig{
		ChainId:             big.NewInt(1),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		DPoS:                new(DPoSConfig),
	}

	// TestnetChainConfig contains the chain parameters to run a node on the Ropsten test network.
	TestnetChainConfig = &ChainConfig{
		ChainId:             big.NewInt(3),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		DPoS:                new(DPoSConfig),
	}

	// AllCliqueProtocolChanges contains every protocol change (EIPs) introduced
	// and accepted by the Juchain core developers into the Clique consensus.
	//
	// This configuration is intentionally not using keyed fields to force anyone
	// adding flags to the config to also have to set these fields.
	AllCliqueProtocolChanges = &ChainConfig{
		big.NewInt(1337),
		big.NewInt(0),
		big.NewInt(0),
		&CliqueConfig{Period: 0, Epoch: 30000},
		nil}

	TestChainConfig = &ChainConfig{
		big.NewInt(1),
		big.NewInt(0),
		big.NewInt(0),
		nil ,
		new(DPoSConfig)}
	TestRules       = TestChainConfig.Rules(new(big.Int))
)

// ChainConfig is the core config which determines the blockchain settings.
//
// ChainConfig is stored in the database on a per block basis. This means
// that any network, identified by its genesis block, can have its own
// set of configuration options.
type ChainConfig struct {
	ChainId *big.Int `json:"chainId"` // Chain id identifies the current chain and is used for replay protection

	EIP158Block *big.Int `json:"eip158Block,omitempty"` // EIP158 HF block

	ByzantiumBlock      *big.Int `json:"byzantiumBlock,omitempty"`      // Byzantium switch block (nil = no fork, 0 = already on byzantium)

	// Various consensus engines
	Clique *CliqueConfig `json:"clique,omitempty"`
	DPoS   *DPoSConfig   `json:"dpos,omitempty"`
}

type DPoSConfig struct{
	Period   uint64 `json:"period"` // Number of seconds between blocks to enforce
	Epoch    uint64 `json:"epoch"`  // Epoch length to reset votes and checkpoint
	PoSMode  Mode
	FakeFail  uint64        // Block number which fails PoS check even in fake mode
	FakeDelay time.Duration // Time delay to sleep for before returning from verify
}

// only for test purpose
type Mode uint
const (
	ModeFake Mode = iota
	ModeFullFake
)

func (c *DPoSConfig) String() string {
	return "dpos"
}

// CliqueConfig is the consensus engine configs for proof-of-authority based sealing.
type CliqueConfig struct {
	Period uint64 `json:"period"` // Number of seconds between blocks to enforce
	Epoch  uint64 `json:"epoch"`  // Epoch length to reset votes and checkpoint
}

// String implements the stringer interface, returning the consensus engine details.
func (c *CliqueConfig) String() string {
	return "clique"
}

// String implements the fmt.Stringer interface.
func (c *ChainConfig) String() string {
	var engine interface{}
	switch {
	case c.DPoS != nil:
		engine = c.DPoS
	case c.Clique != nil:
		engine = c.Clique
	default:
		engine = &DPoSConfig{};
	}
	return fmt.Sprintf("{ChainID: %v, Engine: %v}",
		c.ChainId,
		engine,
	)
}

func (c *ChainConfig) IsByzantium(num *big.Int) bool {
	return isForked(c.ByzantiumBlock, num)
}

// GasTable returns the gas table corresponding to the current phase (homestead or homestead reprice).
//
// The returned GasTable's fields shouldn't, under any circumstances, be changed.
func (c *ChainConfig) GasTable(num *big.Int) GasTable {
	return GasTableEIP158;
}

// CheckCompatible checks whether scheduled fork transitions have been imported
// with a mismatching chain configuration.
func (c *ChainConfig) CheckCompatible(newcfg *ChainConfig, height uint64) *ConfigCompatError {
	bhead := new(big.Int).SetUint64(height)

	// Iterate checkCompatible to find the lowest conflict.
	var lasterr *ConfigCompatError
	for {
		err := c.checkCompatible(newcfg, bhead)
		if err == nil || (lasterr != nil && err.RewindTo == lasterr.RewindTo) {
			break
		}
		lasterr = err
		bhead.SetUint64(err.RewindTo)
	}
	return lasterr
}

func (c *ChainConfig) checkCompatible(newcfg *ChainConfig, head *big.Int) *ConfigCompatError {
	if isForkIncompatible(c.ByzantiumBlock, newcfg.ByzantiumBlock, head) {
		return newCompatError("Byzantium fork block", c.ByzantiumBlock, newcfg.ByzantiumBlock)
	}
	return nil
}

type DAppAddress struct {
	Addresse []common.Address;
}

func (c *DAppAddress) Has(dappAddr *common.Address) bool {
	return true;
}

func (c *DAppAddress) HasDiskSpace(dappAddr *common.Address) bool {
	return true;
}

func (c *DAppAddress) GetAssignedNodes(dappAddr *common.Address) bool {
	return false;
}

func (c *DAppAddress) ToString() []string {
	array := make([]string, len(c.Addresse))
	for i := range c.Addresse {
		array[i] = c.Addresse[i].String()
	}
	return array;
}

// isForkIncompatible returns true if a fork scheduled at s1 cannot be rescheduled to
// block s2 because head is already past the fork.
func isForkIncompatible(s1, s2, head *big.Int) bool {
	return (isForked(s1, head) || isForked(s2, head)) && !configNumEqual(s1, s2)
}

// isForked returns whether a fork scheduled at block s is active at the given head block.
func isForked(s, head *big.Int) bool {
	if s == nil || head == nil {
		return false
	}
	return s.Cmp(head) <= 0
}

func configNumEqual(x, y *big.Int) bool {
	if x == nil {
		return y == nil
	}
	if y == nil {
		return x == nil
	}
	return x.Cmp(y) == 0
}

// ConfigCompatError is raised if the locally-stored blockchain is initialised with a
// ChainConfig that would alter the past.
type ConfigCompatError struct {
	What string
	// block numbers of the stored and new configurations
	StoredConfig, NewConfig *big.Int
	// the block number to which the local chain must be rewound to correct the error
	RewindTo uint64
}

func newCompatError(what string, storedblock, newblock *big.Int) *ConfigCompatError {
	var rew *big.Int
	switch {
	case storedblock == nil:
		rew = newblock
	case newblock == nil || storedblock.Cmp(newblock) < 0:
		rew = storedblock
	default:
		rew = newblock
	}
	err := &ConfigCompatError{what, storedblock, newblock, 0}
	if rew != nil && rew.Sign() > 0 {
		err.RewindTo = rew.Uint64() - 1
	}
	return err
}

func (err *ConfigCompatError) Error() string {
	return fmt.Sprintf("mismatching %s in database (have %d, want %d, rewindto %d)", err.What, err.StoredConfig, err.NewConfig, err.RewindTo)
}

// Rules wraps ChainConfig and is merely syntatic sugar or can be used for functions
// that do not have or require information about the block.
//
// Rules is a one time interface meaning that it shouldn't be used in between transition
// phases.
type Rules struct {
	ChainId     *big.Int
	IsEIP158     bool
	IsByzantium  bool
}

func (c *ChainConfig) Rules(num *big.Int) Rules {
	chainId := c.ChainId
	if chainId == nil {
		chainId = new(big.Int)
	}
	return Rules{ChainId: new(big.Int).Set(chainId), IsEIP158: true, IsByzantium: c.IsByzantium(num)}
}
