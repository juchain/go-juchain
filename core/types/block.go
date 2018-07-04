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

// Package types contains data types related to Juchain consensus.
package types

import (
	"encoding/binary"
	"io"
	"math/big"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/common/hexutil"
	"github.com/juchain/go-juchain/common/crypto/sha3"
	"github.com/juchain/go-juchain/common/rlp"
	"github.com/juchain/go-juchain/common/log"
	"fmt"
	"encoding/json"
	"errors"
)

var (
	EmptyRootHash  = DeriveSha(Transactions{})
	EmptyUncleHash = CalcUncleHash(nil)
)

// A BlockNonce is a 64-bit hash which proves (combined with the
// mix-hash) that a sufficient amount of computation has been carried
// out on a block.
type BlockNonce [8]byte

// EncodeNonce converts the given integer to a block nonce.
func EncodeNonce(i uint64) BlockNonce {
	var n BlockNonce
	binary.BigEndian.PutUint64(n[:], i)
	return n
}

// Uint64 returns the integer value of a block nonce.
func (n BlockNonce) Uint64() uint64 {
	return binary.BigEndian.Uint64(n[:])
}

// MarshalText encodes n as a hex string with 0x prefix.
func (n BlockNonce) MarshalText() ([]byte, error) {
	return hexutil.Bytes(n[:]).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (n *BlockNonce) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("BlockNonce", input, n[:])
}

// Header represents a block header in the Juchain main blockchain.
type Header struct {
	ParentHash  common.Hash    `json:"parentHash"       gencodec:"required"`
	UncleHash   common.Hash    `json:"sha3Uncles"       gencodec:"required"`
	Coinbase    common.Address `json:"miner"            gencodec:"required"`
	Root        common.Hash    `json:"stateRoot"        gencodec:"required"`
	TxHash      common.Hash    `json:"transactionsRoot" gencodec:"required"`
	ReceiptHash common.Hash    `json:"receiptsRoot"     gencodec:"required"`
	Bloom       Bloom          `json:"logsBloom"        gencodec:"required"`
	Difficulty  *big.Int       `json:"difficulty"       gencodec:"required"`
	Number      *big.Int       `json:"number"           gencodec:"required"`
	GasLimit    uint64         `json:"gasLimit"         gencodec:"required"`
	GasUsed     uint64         `json:"gasUsed"          gencodec:"required"`
	Time        *big.Int       `json:"timestamp"        gencodec:"required"`
	Extra       []byte         `json:"extraData"        gencodec:"required"`
	MixDigest   common.Hash    `json:"mixHash"          gencodec:"required"`
	Nonce       BlockNonce     `json:"nonce"            gencodec:"required"`
	Round       uint64         `json:"round"            gencodec:"required"` // DPoS support: the round number.
	Round2      uint64         `json:"round2"           gencodec:"required"` // DPoS support: the round2 number if round number is exceeded.
	PresidentId string         `json:"presidentId"      gencodec:"required"` // DPoS support: the president id.
}

// field type overrides for gencodec
type headerMarshaling struct {
	Difficulty *hexutil.Big
	Number     *hexutil.Big
	GasLimit   hexutil.Uint64
	GasUsed    hexutil.Uint64
	Time       *hexutil.Big
	Extra      hexutil.Bytes
	Hash       common.Hash `json:"hash"` // adds call to Hash() in MarshalJSON
}

// Hash returns the block hash of the header, which is simply the keccak256 hash of its
// RLP encoding.
func (h *Header) Hash() common.Hash {
	return rlpHash(h)
}

var _ = (*headerMarshaling)(nil)

func (h Header) MarshalJSON() ([]byte, error) {
	type Header struct {
		ParentHash  common.Hash    `json:"parentHash"       gencodec:"required"`
		UncleHash   common.Hash    `json:"sha3Uncles"       gencodec:"required"`
		Coinbase    common.Address `json:"miner"            gencodec:"required"`
		Root        common.Hash    `json:"stateRoot"        gencodec:"required"`
		TxHash      common.Hash    `json:"transactionsRoot" gencodec:"required"`
		ReceiptHash common.Hash    `json:"receiptsRoot"     gencodec:"required"`
		Bloom       Bloom          `json:"logsBloom"        gencodec:"required"`
		Difficulty  *hexutil.Big   `json:"difficulty"       gencodec:"required"`
		Number      *hexutil.Big   `json:"number"           gencodec:"required"`
		GasLimit    hexutil.Uint64 `json:"gasLimit"         gencodec:"required"`
		GasUsed     hexutil.Uint64 `json:"gasUsed"          gencodec:"required"`
		Time        *hexutil.Big   `json:"timestamp"        gencodec:"required"`
		Extra       hexutil.Bytes  `json:"extraData"        gencodec:"required"`
		MixDigest   common.Hash    `json:"mixHash"          gencodec:"required"`
		Nonce       BlockNonce     `json:"nonce"            gencodec:"required"`
		Round       uint64         `json:"round"            gencodec:"required"`
		Round2      uint64         `json:"round2"           gencodec:"required"`
		PresidentId string         `json:"presidentId"      gencodec:"required"`
		Hash        common.Hash    `json:"hash"`
	}
	var enc Header
	enc.ParentHash = h.ParentHash
	enc.UncleHash = h.UncleHash
	enc.Coinbase = h.Coinbase
	enc.Root = h.Root
	enc.TxHash = h.TxHash
	enc.ReceiptHash = h.ReceiptHash
	enc.Bloom = h.Bloom
	enc.Difficulty = (*hexutil.Big)(h.Difficulty)
	enc.Number = (*hexutil.Big)(h.Number)
	enc.GasLimit = hexutil.Uint64(h.GasLimit)
	enc.GasUsed = hexutil.Uint64(h.GasUsed)
	enc.Time = (*hexutil.Big)(h.Time)
	enc.Extra = h.Extra
	enc.MixDigest = h.MixDigest
	enc.Nonce = h.Nonce
	enc.Hash = h.Hash()
	return json.Marshal(&enc)
}

func (h *Header) UnmarshalJSON(input []byte) error {
	type Header struct {
		ParentHash  *common.Hash    `json:"parentHash"       gencodec:"required"`
		UncleHash   *common.Hash    `json:"sha3Uncles"       gencodec:"required"`
		Coinbase    *common.Address `json:"miner"            gencodec:"required"`
		Root        *common.Hash    `json:"stateRoot"        gencodec:"required"`
		TxHash      *common.Hash    `json:"transactionsRoot" gencodec:"required"`
		ReceiptHash *common.Hash    `json:"receiptsRoot"     gencodec:"required"`
		Bloom       *Bloom          `json:"logsBloom"        gencodec:"required"`
		Difficulty  *hexutil.Big    `json:"difficulty"       gencodec:"required"`
		Number      *hexutil.Big    `json:"number"           gencodec:"required"`
		GasLimit    *hexutil.Uint64 `json:"gasLimit"         gencodec:"required"`
		GasUsed     *hexutil.Uint64 `json:"gasUsed"          gencodec:"required"`
		Time        *hexutil.Big    `json:"timestamp"        gencodec:"required"`
		Extra       *hexutil.Bytes  `json:"extraData"        gencodec:"required"`
		MixDigest   *common.Hash    `json:"mixHash"          gencodec:"required"`
		Nonce       *BlockNonce     `json:"nonce"            gencodec:"required"`
		Round       *uint64         `json:"round"            gencodec:"required"`
		Round2      *uint64         `json:"round2"           gencodec:"required"`
		PresidentId *string         `json:"presidentId"      gencodec:"required"`
	}
	var dec Header
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.ParentHash == nil {
		return errors.New("missing required field 'parentHash' for Header")
	}
	h.Round = *dec.Round
	h.Round2 = *dec.Round2
	h.PresidentId = *dec.PresidentId
	if dec.UncleHash == nil {
		return errors.New("missing required field 'PresidentId' for Header")
	}
	h.ParentHash = *dec.ParentHash
	if dec.UncleHash == nil {
		return errors.New("missing required field 'sha3Uncles' for Header")
	}
	h.UncleHash = *dec.UncleHash
	if dec.Coinbase == nil {
		return errors.New("missing required field 'miner' for Header")
	}
	h.Coinbase = *dec.Coinbase
	if dec.Root == nil {
		return errors.New("missing required field 'stateRoot' for Header")
	}
	h.Root = *dec.Root
	if dec.TxHash == nil {
		return errors.New("missing required field 'transactionsRoot' for Header")
	}
	h.TxHash = *dec.TxHash
	if dec.ReceiptHash == nil {
		return errors.New("missing required field 'receiptsRoot' for Header")
	}
	h.ReceiptHash = *dec.ReceiptHash
	if dec.Bloom == nil {
		return errors.New("missing required field 'logsBloom' for Header")
	}
	h.Bloom = *dec.Bloom
	if dec.Difficulty == nil {
		return errors.New("missing required field 'difficulty' for Header")
	}
	h.Difficulty = (*big.Int)(dec.Difficulty)
	if dec.Number == nil {
		return errors.New("missing required field 'number' for Header")
	}
	h.Number = (*big.Int)(dec.Number)
	if dec.GasLimit == nil {
		return errors.New("missing required field 'gasLimit' for Header")
	}
	h.GasLimit = uint64(*dec.GasLimit)
	if dec.GasUsed == nil {
		return errors.New("missing required field 'gasUsed' for Header")
	}
	h.GasUsed = uint64(*dec.GasUsed)
	if dec.Time == nil {
		return errors.New("missing required field 'timestamp' for Header")
	}
	h.Time = (*big.Int)(dec.Time)
	if dec.Extra == nil {
		return errors.New("missing required field 'extraData' for Header")
	}
	h.Extra = *dec.Extra
	if dec.MixDigest == nil {
		return errors.New("missing required field 'mixHash' for Header")
	}
	h.MixDigest = *dec.MixDigest
	if dec.Nonce == nil {
		return errors.New("missing required field 'nonce' for Header")
	}
	h.Nonce = *dec.Nonce
	return nil
}

// HashNoNonce returns the hash which is used as input for the proof-of-work search.
func (h *Header) HashNoNonce() common.Hash {
	return rlpHash([]interface{}{
		h.ParentHash,
		h.UncleHash,
		h.Coinbase,
		h.Root,
		h.TxHash,
		h.ReceiptHash,
		h.Bloom,
		h.Difficulty,
		h.Number,
		h.GasLimit,
		h.GasUsed,
		h.Time,
		h.Extra,
		h.Round,
		h.Round2,
		h.PresidentId,
	})
}

// Size returns the approximate memory used by all internal contents. It is used
// to approximate and limit the memory consumption of various caches.
func (h *Header) Size() common.StorageSize {
	return common.StorageSize(unsafe.Sizeof(*h)) + common.StorageSize(len(h.Extra)+(h.Difficulty.BitLen()+h.Number.BitLen()+h.Time.BitLen())/8)
}

func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

// Body is a simple (mutable, non-safe) data container for storing and moving
// a block's data contents (transactions and uncles) together.
type Body struct {
	Transactions []*Transaction
	Uncles       []*Header
}

// Block represents an entire block in the Juchain blockchain.
type Block struct {
	header       *Header
	uncles       []*Header
	transactions Transactions

	// caches
	hash atomic.Value
	size atomic.Value

	// Td is used by package core to store the total difficulty
	// of the chain up to and including the block.
	td *big.Int

	// These fields are used by package eth to track
	// inter-peer block relay.
	ReceivedAt   time.Time
	ReceivedFrom interface{}
}

// DeprecatedTd is an old relic for extracting the TD of a block. It is in the
// code solely to facilitate upgrading the database from the old format to the
// new, after which it should be deleted. Do not use!
func (b *Block) DeprecatedTd() *big.Int {
	return b.td
}

// [deprecated by eth/63]
// StorageBlock defines the RLP encoding of a Block stored in the
// state database. The StorageBlock encoding contains fields that
// would otherwise need to be recomputed.
type StorageBlock Block

// "external" block encoding. used for eth protocol, etc.
type extblock struct {
	Header *Header
	Txs    []*Transaction
	Uncles []*Header
}

// [deprecated by eth/63]
// "storage" block encoding. used for database.
type storageblock struct {
	Header *Header
	Txs    []*Transaction
	Uncles []*Header
	TD     *big.Int
}

// NewBlock creates a new block. The input data is copied,
// changes to header and to the field values will not affect the
// block.
//
// The values of TxHash, UncleHash, ReceiptHash and Bloom in header
// are ignored and set to values derived from the given txs, uncles
// and receipts.
func NewBlock(header *Header, txs []*Transaction, uncles []*Header, receipts []*Receipt) *Block {
	b := &Block{header: CopyHeader(header), td: new(big.Int)}

	// TODO: panic if len(txs) != len(receipts)
	if len(txs) == 0 {
		b.header.TxHash = EmptyRootHash
		//log.Info("new empty txhash: " + EmptyRootHash.String())
	} else {
		b.header.TxHash = DeriveSha(Transactions(txs))
		b.transactions = make(Transactions, len(txs))
		copy(b.transactions, txs)
	}

	if len(receipts) == 0 {
		b.header.ReceiptHash = EmptyRootHash
	} else {
		b.header.ReceiptHash = DeriveSha(Receipts(receipts))
		b.header.Bloom = CreateBloom(receipts)
	}

	if len(uncles) == 0 {
		b.header.UncleHash = EmptyUncleHash
	} else {
		b.header.UncleHash = CalcUncleHash(uncles)
		b.uncles = make([]*Header, len(uncles))
		for i := range uncles {
			b.uncles[i] = CopyHeader(uncles[i])
		}
	}

	return b
}

// NewBlockWithHeader creates a block with the given header data. The
// header data is copied, changes to header and to the field values
// will not affect the block.
func NewBlockWithHeader(header *Header) *Block {
	return &Block{header: CopyHeader(header)}
}

// CopyHeader creates a deep copy of a block header to prevent side effects from
// modifying a header variable.
func CopyHeader(h *Header) *Header {
	cpy := *h
	if cpy.Time = new(big.Int); h.Time != nil {
		cpy.Time.Set(h.Time)
	}
	if cpy.Difficulty = new(big.Int); h.Difficulty != nil {
		cpy.Difficulty.Set(h.Difficulty)
	}
	if cpy.Number = new(big.Int); h.Number != nil {
		cpy.Number.Set(h.Number)
	}
	if len(h.Extra) > 0 {
		cpy.Extra = make([]byte, len(h.Extra))
		copy(cpy.Extra, h.Extra)
	}
	return &cpy
}

// DecodeRLP decodes the Juchain
func (b *Block) DecodeRLP(s *rlp.Stream) error {
	var eb extblock
	_, size, _ := s.Kind()
	if err := s.Decode(&eb); err != nil {
		return err
	}
	b.header, b.uncles, b.transactions = eb.Header, eb.Uncles, eb.Txs
	b.size.Store(common.StorageSize(rlp.ListSize(size)))
	return nil
}

// EncodeRLP serializes b into the Juchain RLP block format.
func (b *Block) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extblock{
		Header: b.header,
		Txs:    b.transactions,
		Uncles: b.uncles,
	})
}

// [deprecated by eth/63]
func (b *StorageBlock) DecodeRLP(s *rlp.Stream) error {
	var sb storageblock
	if err := s.Decode(&sb); err != nil {
		return err
	}
	b.header, b.uncles, b.transactions, b.td = sb.Header, sb.Uncles, sb.Txs, sb.TD
	return nil
}

// TODO: copies

func (b *Block) Uncles() []*Header          { return b.uncles }
func (b *Block) Transactions() Transactions { return b.transactions }

func (b *Block) Transaction(hash common.Hash) *Transaction {
	for _, transaction := range b.transactions {
		if transaction.Hash() == hash {
			return transaction
		}
	}
	return nil
}

func (b *Block) Number() *big.Int     { return new(big.Int).Set(b.header.Number) }
func (b *Block) GasLimit() uint64     { return b.header.GasLimit }
func (b *Block) GasUsed() uint64      { return b.header.GasUsed }
func (b *Block) Difficulty() *big.Int { return new(big.Int).Set(b.header.Difficulty) }
func (b *Block) Time() *big.Int       { return new(big.Int).Set(b.header.Time) }

func (b *Block) NumberU64() uint64        { return b.header.Number.Uint64() }
func (b *Block) MixDigest() common.Hash   { return b.header.MixDigest }
func (b *Block) Nonce() uint64            { return binary.BigEndian.Uint64(b.header.Nonce[:]) }
func (b *Block) Bloom() Bloom             { return b.header.Bloom }
func (b *Block) Coinbase() common.Address { return b.header.Coinbase }
func (b *Block) Root() common.Hash        { return b.header.Root }
func (b *Block) ParentHash() common.Hash  { return b.header.ParentHash }
func (b *Block) TxHash() common.Hash      { return b.header.TxHash }
func (b *Block) ReceiptHash() common.Hash { return b.header.ReceiptHash }
func (b *Block) UncleHash() common.Hash   { return b.header.UncleHash }
func (b *Block) Extra() []byte            { return common.CopyBytes(b.header.Extra) }
func (b *Block) Round() uint64            { return b.header.Round }
func (b *Block) Round2() uint64            { return b.header.Round2 }
func (b *Block) PesidentID() string       { return b.header.PresidentId }
func (b *Block) Header() *Header          { return CopyHeader(b.header) }
// Body returns the non-header content of the block.
func (b *Block) Body() *Body              { return &Body{b.transactions, b.uncles} }
func (b *Block) HashNoNonce() common.Hash { return b.header.HashNoNonce() }

// Size returns the true RLP encoded storage size of the block, either by encoding
// and returning it, or returning a previsouly cached value.
func (b *Block) Size() common.StorageSize {
	if size := b.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, b)
	b.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

func (b *Block) ToString() {
	log.Info(fmt.Sprintf(`
############### BLOCK INFO #############

Number: %v
Round: %v
PesidentID: %v
Hash: 0x%x
Root: 0x%x
ParentHash: 0x%x
GasLimit: 0x%x
GasUsed: 0x%x
Difficulty: %v
MixDigest: %v
Nonce: %v
Coinbase: %v
TxHash: 0x%x
ReceiptHash: 0x%x
UncleHash: 0x%x

########################################
`, b.Number(), b.Round(), b.PesidentID(), b.Hash(), b.Root(), b.ParentHash(), b.GasLimit(), b.GasUsed(), b.Difficulty(), b.MixDigest(), b.Nonce(), b.Coinbase(), b.TxHash(), b.ReceiptHash(), b.UncleHash()))
}

type writeCounter common.StorageSize

func (c *writeCounter) Write(b []byte) (int, error) {
	*c += writeCounter(len(b))
	return len(b), nil
}

func CalcUncleHash(uncles []*Header) common.Hash {
	return rlpHash(uncles)
}

// WithSeal returns a new block with the data from b but the header replaced with
// the sealed one.
func (b *Block) WithSeal(header *Header) *Block {
	cpy := *header

	return &Block{
		header:       &cpy,
		transactions: b.transactions,
		uncles:       b.uncles,
	}
}

// WithBody returns a new block with the given transaction and uncle contents.
func (b *Block) WithBody(transactions []*Transaction, uncles []*Header) *Block {
	block := &Block{
		header:       CopyHeader(b.header),
		transactions: make([]*Transaction, len(transactions)),
		uncles:       make([]*Header, len(uncles)),
	}
	copy(block.transactions, transactions)
	for i := range uncles {
		block.uncles[i] = CopyHeader(uncles[i])
	}
	return block
}

// Hash returns the keccak256 hash of b's header.
// The hash is computed on the first call and cached thereafter.
func (b *Block) Hash() common.Hash {
	if hash := b.hash.Load(); hash != nil {
		return hash.(common.Hash)
	}
	v := b.header.Hash()
	b.hash.Store(v)
	return v
}

type Blocks []*Block

type BlockBy func(b1, b2 *Block) bool

func (self BlockBy) Sort(blocks Blocks) {
	bs := blockSorter{
		blocks: blocks,
		by:     self,
	}
	sort.Sort(bs)
}

type blockSorter struct {
	blocks Blocks
	by     func(b1, b2 *Block) bool
}

func (self blockSorter) Len() int { return len(self.blocks) }
func (self blockSorter) Swap(i, j int) {
	self.blocks[i], self.blocks[j] = self.blocks[j], self.blocks[i]
}
func (self blockSorter) Less(i, j int) bool { return self.by(self.blocks[i], self.blocks[j]) }

func Number(b1, b2 *Block) bool { return b1.header.Number.Cmp(b2.header.Number) < 0 }


//--------------DApp block structure--------------------//

// Header represents a block header in the DApp blockchain.
type DAppHeader struct {
	Number      *big.Int       `json:"number"           gencodec:"required"`
	ParentHash  common.Hash    `json:"parentHash"       gencodec:"required"`
	MainBlockHash  common.Hash `json:"mainBlockHash"    gencodec:"required"` // represents the referring block from main chain.
	Root        common.Hash    `json:"stateRoot"        gencodec:"required"`
	TxHash      common.Hash    `json:"transactionsRoot" gencodec:"required"`
	ReceiptHash common.Hash    `json:"receiptsRoot"     gencodec:"required"`
	Time        *big.Int       `json:"timestamp"        gencodec:"required"`
	MixDigest   common.Hash    `json:"mixHash"          gencodec:"required"`
	Nonce       BlockNonce     `json:"nonce"            gencodec:"required"`
}

// Hash returns the block hash of the header, which is simply the keccak256 hash of its
// RLP encoding.
func (h *DAppHeader) Hash() common.Hash {
	return rlpHash(h)
}

// HashNoNonce returns the hash which is used as input for the proof-of-work search.
func (h *DAppHeader) HashNoNonce() common.Hash {
	return rlpHash([]interface{}{
		h.Number,
		h.ParentHash,
		h.MainBlockHash,
		h.Root,
		h.TxHash,
		h.ReceiptHash,
		h.Time,
		h.MixDigest,
		h.Nonce,
	})
}

func (h DAppHeader) MarshalJSON() ([]byte, error) {
	type Header struct {
		ParentHash  common.Hash    `json:"parentHash"       gencodec:"required"`
		UncleHash   common.Hash    `json:"sha3Uncles"       gencodec:"required"`
		Root        common.Hash    `json:"stateRoot"        gencodec:"required"`
		MainBlockHash  common.Hash `json:"mainBlockHash"    gencodec:"required"`
		TxHash      common.Hash    `json:"transactionsRoot" gencodec:"required"`
		ReceiptHash common.Hash    `json:"receiptsRoot"     gencodec:"required"`
		Number      *hexutil.Big   `json:"number"           gencodec:"required"`
		Time        *hexutil.Big   `json:"timestamp"        gencodec:"required"`
		MixDigest   common.Hash    `json:"mixHash"          gencodec:"required"`
		Nonce       BlockNonce     `json:"nonce"            gencodec:"required"`
		Hash        common.Hash    `json:"hash"`
	}
	var enc Header
	enc.ParentHash = h.ParentHash
	enc.Root = h.Root
	enc.MainBlockHash = h.MainBlockHash
	enc.TxHash = h.TxHash
	enc.ReceiptHash = h.ReceiptHash
	enc.Number = (*hexutil.Big)(h.Number)
	enc.Time = (*hexutil.Big)(h.Time)
	enc.MixDigest = h.MixDigest
	enc.Nonce = h.Nonce
	enc.Hash = h.Hash()
	return json.Marshal(&enc)
}

func (h *DAppHeader) UnmarshalJSON(input []byte) error {
	type Header struct {
		ParentHash  *common.Hash    `json:"parentHash"       gencodec:"required"`
		UncleHash   *common.Hash    `json:"sha3Uncles"       gencodec:"required"`
		Root        *common.Hash    `json:"stateRoot"        gencodec:"required"`
		MainBlockHash  *common.Hash `json:"mainBlockHash"    gencodec:"required"`
		TxHash      *common.Hash    `json:"transactionsRoot" gencodec:"required"`
		ReceiptHash *common.Hash    `json:"receiptsRoot"     gencodec:"required"`
		Number      *hexutil.Big   `json:"number"           gencodec:"required"`
		Time        *hexutil.Big   `json:"timestamp"        gencodec:"required"`
		MixDigest   *common.Hash    `json:"mixHash"          gencodec:"required"`
		Nonce       *BlockNonce     `json:"nonce"            gencodec:"required"`
		Hash        *common.Hash    `json:"hash"`
	}
	var dec Header
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.ParentHash == nil {
		return errors.New("missing required field 'parentHash' for Header")
	}
	h.ParentHash = *dec.ParentHash
	if dec.UncleHash == nil {
		return errors.New("missing required field 'sha3Uncles' for Header")
	}
	h.Root = *dec.Root
	if dec.MainBlockHash == nil {
		return errors.New("missing required field 'transactionsRoot' for Header")
	}
	h.MainBlockHash = *dec.MainBlockHash
	if dec.TxHash == nil {
		return errors.New("missing required field 'transactionsRoot' for Header")
	}
	h.TxHash = *dec.TxHash
	if dec.ReceiptHash == nil {
		return errors.New("missing required field 'receiptsRoot' for Header")
	}
	h.ReceiptHash = *dec.ReceiptHash
	h.Number = (*big.Int)(dec.Number)
	if dec.Time == nil {
		return errors.New("missing required field 'timestamp' for Header")
	}
	h.Time = (*big.Int)(dec.Time)
	h.MixDigest = *dec.MixDigest
	if dec.Nonce == nil {
		return errors.New("missing required field 'nonce' for Header")
	}
	h.Nonce = *dec.Nonce
	return nil
}


// Block represents an entire block in the Juchain blockchain.
type DAppBlock struct {
	header       *DAppHeader
	transactions Transactions
	// caches
	hash atomic.Value
	size atomic.Value
}

// "external" block encoding. used for eth protocol, etc.
type extblock1 struct {
	Header *DAppHeader
	Txs    []*Transaction
}

// DecodeRLP decodes the DApp block
func (b *DAppBlock) DecodeRLP(s *rlp.Stream) error {
	var eb extblock1
	_, size, _ := s.Kind()
	if err := s.Decode(&eb); err != nil {
		return err
	}
	b.header, b.transactions = eb.Header, eb.Txs
	b.size.Store(common.StorageSize(rlp.ListSize(size)))
	return nil
}

// EncodeRLP serializes b into the Juchain RLP block format.
func (b *DAppBlock) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extblock1{
		Header: b.header,
		Txs:    b.transactions,
	})
}

func (b *DAppBlock) Transactions() Transactions { return b.transactions }

func (b *DAppBlock) Transaction(hash common.Hash) *Transaction {
	for _, transaction := range b.transactions {
		if transaction.Hash() == hash {
			return transaction
		}
	}
	return nil
}

func (b *DAppBlock) Number() *big.Int     { return new(big.Int).Set(b.header.Number) }
func (b *DAppBlock) Time() *big.Int       { return new(big.Int).Set(b.header.Time) }

func (b *DAppBlock) NumberU64() uint64        { return b.header.Number.Uint64() }
func (b *DAppBlock) MixDigest() common.Hash   { return b.header.MixDigest }
func (b *DAppBlock) Nonce() uint64            { return binary.BigEndian.Uint64(b.header.Nonce[:]) }
func (b *DAppBlock) Root() common.Hash        { return b.header.Root }
func (b *DAppBlock) ParentHash() common.Hash  { return b.header.ParentHash }
func (b *DAppBlock) MainBlockHash() common.Hash  { return b.header.MainBlockHash }
func (b *DAppBlock) TxHash() common.Hash      { return b.header.TxHash }
func (b *DAppBlock) ReceiptHash() common.Hash { return b.header.ReceiptHash }
func (b *DAppBlock) Header() *DAppHeader      { return CopyDAppHeader(b.header) }
// Body returns the non-header content of the block.
func (b *DAppBlock) Body() *Transactions              { return &b.transactions }
func (b *DAppBlock) HashNoNonce() common.Hash { return b.header.HashNoNonce() }

// Size returns the true RLP encoded storage size of the block, either by encoding
// and returning it, or returning a previsouly cached value.
func (b *DAppBlock) Size() common.StorageSize {
	if size := b.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, b)
	b.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

func (b *DAppBlock) ToString() {
	log.Info(fmt.Sprintf(`
############### DApp BLOCK INFO #############

Number: %v
Root: 0x%x
ParentHash: 0x%x
MainBlockHash: 0x%x
MixDigest: %v
Nonce: %
TxHash: 0x%x
ReceiptHash: 0x%x

#############################################
`, b.Number(), b.Root(), b.ParentHash(), b.MainBlockHash(), b.MixDigest(), b.Nonce(), b.TxHash(), b.ReceiptHash()))
}

// NewBlockWithHeader creates a block with the given header data. The
// header data is copied, changes to header and to the field values
// will not affect the block.
func NewDAppBlockWithHeader(header *DAppHeader) *DAppBlock {
	return &DAppBlock{header: CopyDAppHeader(header)}
}

// CopyHeader creates a deep copy of a block header to prevent side effects from
// modifying a header variable.
func CopyDAppHeader(h *DAppHeader) *DAppHeader {
	cpy := *h
	if cpy.Time = new(big.Int); h.Time != nil {
		cpy.Time.Set(h.Time)
	}
	if cpy.Number = new(big.Int); h.Number != nil {
		cpy.Number.Set(h.Number)
	}
	return &cpy
}