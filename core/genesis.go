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
var DAPPContractBinCode = "608060405234801561001057600080fd5b50610a07806100206000396000f3006080604052600436106100565763ffffffff7c010000000000000000000000000000000000000000000000000000000060003504166364073385811461005b578063ce9925c3146101f8578063d98f356b1461024f575b600080fd5b6040805160206004803580820135601f81018490048402850184019095528484526101f694369492936024939284019190819084018382808284375050604080516020601f89358b018035918201839004830284018301909452808352979a99988101979196509182019450925082915084018382808284375050604080516020601f89358b018035918201839004830284018301909452808352979a99988101979196509182019450925082915084018382808284375050604080516020601f60608a01358b0180359182018390048302840183018552818452989b60ff8b3581169c848d01359091169b958601359a91995097506080909401955091935091820191819084018382808284375050604080516020601f89358b018035918201839004830284018301909452808352979a99988101979196509182019450925082915084018382808284375050604080516020601f89358b018035918201839004830284018301909452808352979a9998810197919650918201945092508291508401838280828437509497506104129650505050505050565b005b6040805160206004803580820135601f81018490048402850184019095528484526101f69436949293602493928401919081908401838280828437509497505050833560ff1694505050602090910135905061062e565b34801561025b57600080fd5b5061027d73ffffffffffffffffffffffffffffffffffffffff600435166106c5565b6040805173ffffffffffffffffffffffffffffffffffffffff8b16815260ff808816608083015286811660a0830152851660c082015260e08101849052610100810183905261012060208083018281528c51928401929092528b5192939192918401916060850191610140860191908e019080838360005b8381101561030d5781810151838201526020016102f5565b50505050905090810190601f16801561033a5780820380516001836020036101000a031916815260200191505b5084810383528b5181528b516020918201918d019080838360005b8381101561036d578181015183820152602001610355565b50505050905090810190601f16801561039a5780820380516001836020036101000a031916815260200191505b5084810382528a5181528a516020918201918c019080838360005b838110156103cd5781810151838201526020016103b5565b50505050905090810190601f1680156103fa5780820380516001836020036101000a031916815260200191505b509c5050505050505050505050505060405180910390f35b336000908152602081905260409020600b015460ff161561043257600080fd5b604080516101a0810182523380825260208083018d81528385018d9052606084018c905260ff8b811660808601528a1660a085015260c08401899052600160e085018190526101008501899052610120850188905261014085018790524261016086015261018085018190526000938452838352949092208351815473ffffffffffffffffffffffffffffffffffffffff191673ffffffffffffffffffffffffffffffffffffffff90911617815591518051939492936104f9938501929190910190610940565b5060408201518051610515916002840191602090910190610940565b5060608201518051610531916003840191602090910190610940565b50608082015160048201805460a085015160ff1991821660ff9485161761ff00191661010091851682021790925560c0850151600585015560e08501516006850180549092169316929092179091558201518051610599916007840191602090910190610940565b5061012082015180516105b6916008840191602090910190610940565b5061014082015180516105d3916009840191602090910190610940565b50610160820151600a82015561018090910151600b909101805460ff19169115159190911790556040517f6142ff54c11c1eb59d0a251a917b38ccd85c84dc4c83c021e8662a73d557b00590600090a1505050505050505050565b336000908152602081905260408120600b015460ff16151560011461065257600080fd5b503360009081526020818152604090912084519091610678916003840191870190610940565b5060048101805461ff00191661010060ff861602179055600581018290556040517f3c22cfbecb928778078fb52ac2ece267e23d58f14308b86f1645e39aaad0197890600090a150505050565b73ffffffffffffffffffffffffffffffffffffffff81166000908152602081905260408120600b01546060908190819084908190819081908190819060ff16151560011461071257600080fd5b5073ffffffffffffffffffffffffffffffffffffffff8a811660009081526020818152604091829020805460048201546006830154600a8401546005850154600180870180548a51600261010094831615850260001901909216829004601f81018c90048c0282018c01909c528b8152989b97909716999098968b019760038c019760ff80891698949094048416969390931694939290918a918301828280156107fd5780601f106107d2576101008083540402835291602001916107fd565b820191906000526020600020905b8154815290600101906020018083116107e057829003601f168201915b50508a5460408051602060026001851615610100026000190190941693909304601f8101849004840282018401909252818152959d508c94509250840190508282801561088b5780601f106108605761010080835404028352916020019161088b565b820191906000526020600020905b81548152906001019060200180831161086e57829003601f168201915b5050895460408051602060026001851615610100026000190190941693909304601f8101849004840282018401909252818152959c508b9450925084019050828280156109195780601f106108ee57610100808354040283529160200191610919565b820191906000526020600020905b8154815290600101906020018083116108fc57829003601f168201915b50505050509550995099509950995099509950995099509950509193959799909294969850565b828054600181600116156101000203166002900490600052602060002090601f016020900481019282601f1061098157805160ff19168380011785556109ae565b828001600101855582156109ae579182015b828111156109ae578251825591602001919060010190610993565b506109ba9291506109be565b5090565b6109d891905b808211156109ba57600081556001016109c4565b905600a165627a7a723058208c10352e639500b0bc4bcdf1027b0cdf5896e3b0cd3cd90144d0ff71ff31c5020029";
var DAPPContractAddress common.Address;
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
	root := statedb.IntermediateRoot(false)
	head.Root = root;
	// commit state into db.
	statedb.Commit(false)
	statedb.Database().TrieDB().Commit(root, true)

	b := types.NewBlock(head, []*types.Transaction{dapptx}, nil, []*types.Receipt{receipt})
	log.Info("Created Genesis Block.");
	b.ToString();
	log.Info("Installed DApp decentralized manager. Address: " + receipt.ContractAddress.String())

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
