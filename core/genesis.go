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
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/common/hexutil"
	"github.com/juchain/go-juchain/common/math"
	"github.com/juchain/go-juchain/core/state"
	"github.com/juchain/go-juchain/core/types"
	"github.com/juchain/go-juchain/core/store"
	"github.com/juchain/go-juchain/common/log"
	"github.com/juchain/go-juchain/config"
	"github.com/juchain/go-juchain/common/rlp"
	"github.com/juchain/go-juchain/vm/solc"
)

//go:generate gencodec -type Genesis -field-override genesisSpecMarshaling -out gen_genesis.go
//go:generate gencodec -type GenesisAccount -field-override genesisAccountMarshaling -out gen_genesis_account.go

var errGenesisNoConfig = errors.New("genesis has no chain configuration")

var BlockReward = big.NewInt(5e+18)
// Constants containing the genesis allocation of built-in genesis blocks.
// Their content is an RLP-encoded list of (address, balance) tuples.
// Use mkalloc.go to create/update them.

// nolint: misspell
const mainnetAllocData = ""
const testnetAllocData = ""

// Genesis specifies the header fields, state of a genesis block. It also defines hard
// fork switch-over blocks through the chain configuration.
type Genesis struct {
	Config     *config.ChainConfig `json:"config"`
	Nonce      uint64              `json:"nonce"`
	Timestamp  uint64              `json:"timestamp"`
	ExtraData  []byte              `json:"extraData"`
	GasLimit   uint64              `json:"gasLimit"   gencodec:"required"`
	Difficulty *big.Int            `json:"difficulty" gencodec:"required"`
	Mixhash    common.Hash         `json:"mixHash"`
	Coinbase   common.Address      `json:"coinbase"`
	Alloc      GenesisAlloc        `json:"alloc"      gencodec:"required"`

	// These fields are used for consensus tests. Please don't use them
	// in actual genesis blocks.
	Number     uint64      `json:"number"`
	GasUsed    uint64      `json:"gasUsed"`
	ParentHash common.Hash `json:"parentHash"`
}

// GenesisAlloc specifies the initial state that is part of the genesis block.
type GenesisAlloc map[common.Address]GenesisAccount

func (ga *GenesisAlloc) UnmarshalJSON(data []byte) error {
	m := make(map[common.UnprefixedAddress]GenesisAccount)
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*ga = make(GenesisAlloc)
	for addr, a := range m {
		(*ga)[common.Address(addr)] = a
	}
	return nil
}

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	Code       []byte                      `json:"code,omitempty"`
	Storage    map[common.Hash]common.Hash `json:"storage,omitempty"`
	Balance    *big.Int                    `json:"balance" gencodec:"required"`
	Nonce      uint64                      `json:"nonce,omitempty"`
	PrivateKey []byte                      `json:"secretKey,omitempty"` // for tests
}

// field type overrides for gencodec
type genesisSpecMarshaling struct {
	Nonce      math.HexOrDecimal64
	Timestamp  math.HexOrDecimal64
	ExtraData  hexutil.Bytes
	GasLimit   math.HexOrDecimal64
	GasUsed    math.HexOrDecimal64
	Number     math.HexOrDecimal64
	Difficulty *math.HexOrDecimal256
	Alloc      map[common.UnprefixedAddress]GenesisAccount
}

type genesisAccountMarshaling struct {
	Code       hexutil.Bytes
	Balance    *math.HexOrDecimal256
	Nonce      math.HexOrDecimal64
	Storage    map[storageJSON]storageJSON
	PrivateKey hexutil.Bytes
}

// storageJSON represents a 256 bit byte array, but allows less than 256 bits when
// unmarshaling from hex.
type storageJSON common.Hash

func (h *storageJSON) UnmarshalText(text []byte) error {
	text = bytes.TrimPrefix(text, []byte("0x"))
	if len(text) > 64 {
		return fmt.Errorf("too many hex characters in storage key/value %q", text)
	}
	offset := len(h) - len(text)/2 // pad on the left
	if _, err := hex.Decode(h[offset:], text); err != nil {
		fmt.Println(err)
		return fmt.Errorf("invalid hex storage key/value %q", text)
	}
	return nil
}

func (h storageJSON) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// GenesisMismatchError is raised when trying to overwrite an existing
// genesis block with an incompatible one.
type GenesisMismatchError struct {
	Stored, New common.Hash
}

func (e *GenesisMismatchError) Error() string {
	return fmt.Sprintf("database already contains an incompatible genesis block (have %x, new %x)", e.Stored[:8], e.New[:8])
}

// SetupGenesisBlock writes or updates the genesis block in store.
// The block that will be used is:
//
//                          genesis == nil       genesis != nil
//                       +------------------------------------------
//     store has no genesis |  main-net default  |  genesis
//     store has genesis    |  from DB           |  genesis (if compatible)
//
// The stored chain configuration will be updated if it is compatible (i.e. does not
// specify a fork block below the local head block). In case of a conflict, the
// error is a *config.ConfigCompatError and the new, unwritten config is returned.
//
// The returned chain configuration is never nil.
func SetupGenesisBlock(db store.Database, genesis *Genesis) (*config.ChainConfig, common.Hash, error) {
	if genesis != nil && genesis.Config == nil {
		return config.MainnetChainConfig, common.Hash{}, errGenesisNoConfig
	}

	// Just commit the new block if there is no stored genesis block.
	stored := GetCanonicalHash(db, 0)
	if (stored == common.Hash{}) {
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		block, err := genesis.Commit(db)
		return genesis.Config, block.Hash(), err
	}

	// Check whether the genesis block is already written.
	if genesis != nil {
		hash := genesis.ToBlock(nil).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
	}

	// Get the existing chain configuration.
	newcfg := genesis.configOrDefault(stored)
	storedcfg, err := GetChainConfig(db, stored)
	if err != nil {
		if err == ErrChainConfigNotFound {
			// This case happens if a genesis write was interrupted.
			log.Warn("Found genesis block without chain config")
			err = WriteChainConfig(db, stored, newcfg)
		}
		return newcfg, stored, err
	}
	// Special case: don't change the existing config of a non-mainnet chain if no new
	// config is supplied. These chains would get AllProtocolChanges (and a compat error)
	// if we just continued here.
	//if genesis == nil && stored != config.MainnetGenesisHash {
	//	return storedcfg, stored, nil
	//}

	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	height := GetBlockNumber(db, GetHeadHeaderHash(db))
	compatErr := storedcfg.CheckCompatible(newcfg, height)
	if compatErr != nil && height != 0 && compatErr.RewindTo != 0 {
		return newcfg, stored, compatErr
	}
	return newcfg, stored, WriteChainConfig(db, stored, newcfg)
}

// SetupDAppGenesisBlock writes or updates the dapp genesis block in store.
// The block that will be used is:
//
//                          genesis == nil       genesis != nil
//                       +------------------------------------------
//     store has no genesis |  main-net default  |  genesis
//     store has genesis    |  from DB           |  genesis (if compatible)
//
// The stored chain configuration will be updated if it is compatible (i.e. does not
// specify a fork block below the local head block). In case of a conflict, the
// error is a *config.ConfigCompatError and the new, unwritten config is returned.
//
// The returned chain configuration is never nil.
func SetupDAppGenesisBlock(dappAddress *common.Address, db store.Database, genesis *Genesis) (*config.ChainConfig, common.Hash, error) {
	if genesis != nil && genesis.Config == nil {
		return config.MainnetChainConfig, common.Hash{}, errGenesisNoConfig
	}

	// Just commit the new block if there is no stored genesis block.
	stored := GetCanonicalHash(db, 0)
	if (stored == common.Hash{}) {
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		block, err := genesis.DAppCommit(db, dappAddress)
		return genesis.Config, block.Hash(), err
	}

	// Check whether the genesis block is already written.
	if genesis != nil {
		hash := genesis.ToDAppBlock(nil, dappAddress).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
	}

	// Get the existing chain configuration.
	newcfg := genesis.configOrDefault(stored)
	storedcfg, err := GetChainConfig(db, stored)
	if err != nil {
		if err == ErrChainConfigNotFound {
			// This case happens if a genesis write was interrupted.
			log.Warn("Found genesis block without chain config")
			err = WriteChainConfig(db, stored, newcfg)
		}
		return newcfg, stored, err
	}
	// Special case: don't change the existing config of a non-mainnet chain if no new
	// config is supplied. These chains would get AllProtocolChanges (and a compat error)
	// if we just continued here.
	//if genesis == nil && stored != config.MainnetGenesisHash {
	//	return storedcfg, stored, nil
	//}

	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	height := GetBlockNumber(db, GetHeadHeaderHash(db))
	compatErr := storedcfg.CheckCompatible(newcfg, height)
	if compatErr != nil && height != 0 && compatErr.RewindTo != 0 {
		return newcfg, stored, compatErr
	}
	return newcfg, stored, WriteChainConfig(db, stored, newcfg)
}

func (g *Genesis) configOrDefault(ghash common.Hash) *config.ChainConfig {
	if g != nil && g.Config != nil{
		return g.Config
	}
	return config.MainnetChainConfig
}

// ToBlock creates the genesis block and writes state of a genesis specification
// to the given database (or discards it if nil).
func (g *Genesis) ToBlock(db store.Database) *types.Block {
	if db == nil {
		db, _ = store.NewMemDatabase()
	}
	var dappAddr *common.Address;
	statedb, _ := state.New(common.Hash{}, state.NewDatabase(db))
	for addr, account := range g.Alloc {
		if dappAddr == nil {
			dappAddr = &addr; // set the first address as the creator of dapp contract.
		}
		// if the balance of selected dapp account is zero, will meet gas underpriced exception.
		statedb.AddBalance(addr, account.Balance)
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}
	if dappAddr == nil {
		// if there is no any pre allocated address, set an empty address.
		dappAddr = &common.Address{}
		statedb.AddBalance(*dappAddr, big.NewInt(1000000000000000000))
		// add 1 ether to this account.
	}
	head := &types.Header{
		Number:     new(big.Int).SetUint64(g.Number),
		Nonce:      types.EncodeNonce(g.Nonce),
		Time:       new(big.Int).SetUint64(g.Timestamp),
		ParentHash: g.ParentHash,
		Extra:      g.ExtraData,
		GasLimit:   g.GasLimit,
		GasUsed:    g.GasUsed,
		Difficulty: g.Difficulty,
		MixDigest:  g.Mixhash,
		Coinbase:   g.Coinbase,
	}
	if g.GasLimit == 0 {
		head.GasLimit = config.GenesisGasLimit
	}
	if g.Difficulty == nil {
		head.Difficulty = config.GenesisDifficulty
	}

	// install dapp contract into genesis block of main chain.
	nonce := uint64(0);
	amount := big.NewInt(0);
	gasLimit := head.GasLimit;
	gasPrice := big.NewInt(300000);
	gpool := new(GasPool).AddGas(math.MaxUint64);
	data := []byte(DAPPContractBinCode);
	dapptx := types.NewContractCreation(nonce, amount, gasLimit, gasPrice, data);
	//this special transaction does not need to sign.
	//dapptx, err := types.SignTx(dapptx, types.MakeSigner(config.AllDPoSProtocolChanges, head.Number),nil)
	receipt, _, err := ApplyGenesisTransaction(dappAddr, config.MainnetChainConfig, nil, &head.Coinbase, gpool, statedb, head, dapptx, &head.GasUsed, vm.Config{})
	if err != nil {
		log.Error("Failed to initialize DApp decentralized management contract(ApplyTx)! ", "cause", err.Error())
		return nil
	}

	// install dpos ballot contract into genesis block of main chain.
	data1 := []byte(DPOSBallotBinCode);
	dapptx1 := types.NewContractCreation(uint64(1), amount, gasLimit, gasPrice, data1);
	//this special transaction does not need to sign.
	//dapptx, err := types.SignTx(dapptx, types.MakeSigner(config.AllDPoSProtocolChanges, head.Number),nil)
	receipt1, _, err1 := ApplyGenesisTransaction(dappAddr, config.MainnetChainConfig, nil, &head.Coinbase, gpool, statedb, head, dapptx1, &head.GasUsed, vm.Config{})
	if err1 != nil {
		log.Error("Failed to initialize DPOS ballot contract(ApplyTx)! ", "cause", err1.Error())
		return nil
	}

	root := statedb.IntermediateRoot(false)
	head.Root = root;
	// commit state into db.
	statedb.Commit(false)
	statedb.Database().TrieDB().Commit(root, true)

	b := types.NewBlock(head, []*types.Transaction{dapptx}, nil, []*types.Receipt{receipt})
	log.Info("Created Genesis Block.");
	b.ToString();
	log.Info("Installed DApp decentralized manager. Address: " + receipt.ContractAddress.String())
	log.Info("Installed DPoS ballot manager. Address: " + receipt1.ContractAddress.String())
	DAPPContractAddress = receipt.ContractAddress;
	DPOSBallotContractAddress = receipt1.ContractAddress;

	return b;
}

// Commit writes the block and state of a genesis specification to the database.
// The block is committed as the canonical head block.
func (g *Genesis) Commit(db store.Database) (*types.Block, error) {
	block := g.ToBlock(db)
	if block == nil {
		return nil, fmt.Errorf("Failed to initialize DApp decentralized management contract!")
	}
	if block.Number().Sign() != 0 {
		return nil, fmt.Errorf("can't commit genesis block with number > 0")
	}
	if err := WriteTd(db, block.Hash(), block.NumberU64(), g.Difficulty); err != nil {
		return nil, err
	}
	if err := WriteBlock(db, block); err != nil {
		return nil, err
	}
	if err := WriteBlockReceipts(db, block.Hash(), block.NumberU64(), nil); err != nil {
		return nil, err
	}
	if err := WriteCanonicalHash(db, block.Hash(), block.NumberU64()); err != nil {
		return nil, err
	}
	if err := WriteHeadBlockHash(db, block.Hash()); err != nil {
		return nil, err
	}
	if err := WriteHeadHeaderHash(db, block.Hash()); err != nil {
		return nil, err
	}
	config0 := g.Config
	if config0 == nil {
		config0 = config.MainnetChainConfig
	}
	return block, WriteChainConfig(db, block.Hash(), config0)
}

// MustCommit writes the genesis block and state to store, panicking on error.
// The block is committed as the canonical head block.
func (g *Genesis) MustCommit(db store.Database) *types.Block {
	block, err := g.Commit(db)
	if err != nil {
		panic(err)
	}
	return block
}

// ToBlock creates the genesis block and writes state of a genesis specification
// to the given database (or discards it if nil).
func (g *Genesis) ToDAppBlock(db store.Database, dappAddress *common.Address) *types.Block {
	if db == nil {
		db, _ = store.NewMemDatabase()
	}
	statedb, _ := state.New(common.Hash{}, state.NewDatabase(db))
	head := &types.Header{
		DAppID:     *dappAddress, // should we add DAppMainRoot?
		Number:     new(big.Int).SetUint64(g.Number),
		Nonce:      types.EncodeNonce(g.Nonce),
		Time:       new(big.Int).SetUint64(g.Timestamp),
		ParentHash: g.ParentHash,
		Extra:      g.ExtraData,
		GasLimit:   g.GasLimit,
		GasUsed:    g.GasUsed,
		Difficulty: g.Difficulty,
		MixDigest:  g.Mixhash,
		Coinbase:   g.Coinbase,
	}
	if g.GasLimit == 0 {
		head.GasLimit = config.GenesisGasLimit
	}
	if g.Difficulty == nil {
		head.Difficulty = config.GenesisDifficulty
	}
	root := statedb.IntermediateRoot(false)
	head.Root = root;
	// commit state into db.
	statedb.Commit(false)
	statedb.Database().TrieDB().Commit(root, true)
	b := types.NewBlock(head, nil, nil, nil)
	log.Info("Created DApp Genesis Block.");
	b.ToString();

	return b;
}

func (g *Genesis) DAppCommit(db store.Database, dappAddress *common.Address) (*types.Block, error) {
	block := g.ToDAppBlock(db, dappAddress)
	if block.Number().Sign() != 0 {
		return nil, fmt.Errorf("can't commit genesis block with number > 0")
	}
	if err := WriteTd(db, block.Hash(), block.NumberU64(), g.Difficulty); err != nil {
		return nil, err
	}
	if err := WriteBlock(db, block); err != nil {
		return nil, err
	}
	if err := WriteBlockReceipts(db, block.Hash(), block.NumberU64(), nil); err != nil {
		return nil, err
	}
	if err := WriteCanonicalHash(db, block.Hash(), block.NumberU64()); err != nil {
		return nil, err
	}
	if err := WriteHeadBlockHash(db, block.Hash()); err != nil {
		return nil, err
	}
	if err := WriteHeadHeaderHash(db, block.Hash()); err != nil {
		return nil, err
	}
	config0 := g.Config
	if config0 == nil {
		config0 = config.MainnetChainConfig
	}
	return block, WriteChainConfig(db, block.Hash(), config0)
}

func (g *Genesis) DAppMustCommit(db store.Database, dappAddress *common.Address) *types.Block {
	block, err := g.DAppCommit(db, dappAddress)
	if err != nil {
		panic(err)
	}
	return block
}

// GenesisBlockForTesting creates and writes a block in which addr has the given wei balance.
func GenesisBlockForTesting(db store.Database, addr common.Address, balance *big.Int) *types.Block {
	g := Genesis{Alloc: GenesisAlloc{addr: {Balance: balance}}}
	return g.MustCommit(db)
}

// DefaultGenesisBlock returns the Juchain main net genesis block.
func DefaultGenesisBlock() *Genesis {
	return &Genesis{
		Config:     config.MainnetChainConfig,
		Nonce:      66,
		ExtraData:  hexutil.MustDecode("0x11bbe8db4e347b4e8c937c1c8370e4b5ed33adb3db69cbdb7a38e1e50b1b82fa"),
		GasLimit:   config.GenesisGasLimit,
		Difficulty: config.GenesisDifficulty,
		//Alloc:      decodePrealloc(mainnetAllocData),
	}
}

// DefaultTestnetGenesisBlock returns the test network genesis block.
func DefaultTestnetGenesisBlock() *Genesis {
	return &Genesis{
		Config:     config.TestnetChainConfig,
		Nonce:      66,
		ExtraData:  hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   16777216,
		Difficulty: big.NewInt(1048576),
		//Alloc:      decodePrealloc(testnetAllocData),
	}
}

// DeveloperGenesisBlock returns the 'geth --dev' genesis block. Note, this must
// be seeded with the
func DeveloperGenesisBlock(period uint64, faucet common.Address) *Genesis {
	// Override the default period to the user requested one
	config := *config.AllCliqueProtocolChanges
	config.Clique.Period = period

	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet[:]...), make([]byte, 65)...),
		GasLimit:   6283185,
		Difficulty: big.NewInt(1),
		Alloc: map[common.Address]GenesisAccount{
			common.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			common.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			common.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			common.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			common.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			common.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			common.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			common.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			faucet: {Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))},
		},
	}
}

func decodePrealloc(data string) GenesisAlloc {
	var p []struct{ Addr, Balance *big.Int }
	if err := rlp.NewStream(strings.NewReader(data), 0).Decode(&p); err != nil {
		panic(err)
	}
	ga := make(GenesisAlloc, len(p))
	for _, account := range p {
		ga[common.BigToAddress(account.Addr)] = GenesisAccount{Balance: account.Balance}
	}
	return ga
}
