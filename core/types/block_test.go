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

package types

import (
	"bytes"
	"fmt"
	"math/big"
	"reflect"
	"testing"

	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/common/rlp"
)

// from bcValidBlockTest.json, "SimpleTx"
func TestBlockEncoding(t *testing.T) {

	block0 := &Block{header: &Header{
			Root: common.StringToHash("0xef1552a40b7165c3cd773806b9e0c165b75356e0314bf0706f279c729f51e017"),
			Coinbase: common.StringToAddress("0x8888f1f195afa192cfee860698584c030f4c9db1"),
			MixDigest: common.StringToHash("0xbd4472abb6659ebe3ee06ee4d7b72a00a9f4d001caca51342001075469aff498"),
			Difficulty: big.NewInt(131072),
			GasLimit: uint64(3141592),
			GasUsed: uint64(21000),
			Time: big.NewInt(1426516743),
			Nonce: EncodeNonce(uint64(0xa13a5a8c8f2bb1c4)),
			Round: 0,
			Round2: 0,
			PresidentId: "23432323",
		} };
	blockEnc, err := rlp.EncodeToBytes(&block0)
	if err != nil {
		t.Fatal("encode error: ", err)
	}

	//blockEnc := common.FromHex("f90262f901fba083cafc574e1f51ba9dc0568fc617a08ea2429fb384059c972f13b19fa1c8dd55a01dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347948888f1f195afa192cfee860698584c030f4c9db1a0ef1552a40b7165c3cd773806b9e0c165b75356e0314bf0706f279c729f51e017a05fe50b260da6308036625b850b5d6ced6d0a9f814c0688bc91ffb7b7a3a54b67a0bc37d79753ad738a6dac4921e57392f145d8887476de3f783dfa7edae9283e52b90100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000008302000001832fefd8825208845506eb0780a0bd4472abb6659ebe3ee06ee4d7b72a00a9f4d001caca51342001075469aff49888a13a5a8c8f2bb1c48080f861f85f800a82c35094095e7baea6a6c7c4c2dfeb977efac326af552d870a801ba09bea4c4daac7c7c52e093e6a4c35dbbcf8856f1af7b059ba20253e70848d094fa08a8fae537ce25ed8cb5af9adac3f141af69bd515bd2ba031522df09b97dd72b1c0")
	var block Block
	if err := rlp.DecodeBytes(blockEnc, &block); err != nil {
		t.Fatal("decode error: ", err)
	}

	check := func(f string, got, want interface{}) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s mismatch: got %v, want %v", f, got, want)
		}
	}
	fmt.Println("block hash: " + block.Hash().String())
	check("Difficulty", block.Difficulty(), big.NewInt(131072))
	check("GasLimit", block.GasLimit(), uint64(3141592))
	check("GasUsed", block.GasUsed(), uint64(21000))
	check("Coinbase", block.Coinbase(), common.HexToAddress("0x3836303639383538346330333066346339646231"))
	//fmt.Print(block.Hash().String())
	check("MixDigest", block.MixDigest(), common.HexToHash("0x6139663464303031636163613531333432303031303735343639616666343938"))
	check("Root", block.Root(), common.HexToHash("0x6237353335366530333134626630373036663237396337323966353165303137"))
	check("Hash", block.Hash(), common.HexToHash("0x087055e1c2c2dd923334e576a913d82404a90a16f03abbd3f6f191885f6e87cc"))
	check("Nonce", block.Nonce(), uint64(0xa13a5a8c8f2bb1c4))
	check("Time", block.Time(), big.NewInt(1426516743))
	check("Size", block.Size(), common.StorageSize(len(blockEnc)))
	check("Round", block.Round(), uint64(0))

	tx1 := NewTransaction(0, common.HexToAddress("095e7baea6a6c7c4c2dfeb977efac326af552d87"), big.NewInt(10), 50000, big.NewInt(10), nil)

	tx1, _ = tx1.WithSignature(HomesteadSigner{}, common.Hex2Bytes("9bea4c4daac7c7c52e093e6a4c35dbbcf8856f1af7b059ba20253e70848d094f8a8fae537ce25ed8cb5af9adac3f141af69bd515bd2ba031522df09b97dd72b100"))
	//fmt.Println(block.Transactions()[0].Hash())
	fmt.Println(tx1.data)
	fmt.Println(tx1.Hash())
	//check("len(Transactions)", len(block.Transactions()), 1)
	//check("Transactions[0].Hash", block.Transactions()[0].Hash(), tx1.Hash())

	ourBlockEnc, err := rlp.EncodeToBytes(&block)
	if err != nil {
		t.Fatal("encode error: ", err)
	}
	if !bytes.Equal(ourBlockEnc, blockEnc) {
		t.Errorf("encoded block mismatch:\ngot:  %x\nwant: %x", ourBlockEnc, blockEnc)
	}
}

func TestDAppBlockEncoding(t *testing.T) {

	dappId = common.HexToAddress("095e7baea6a6c7c4c2dfeb977efac326af552d87")
	block := &Block{header: &Header{
		DAppID: dappId,
		Number: big.NewInt(142),
		Root: common.StringToHash("0xef1552a40b7165c3cd773806b9e0c165b75356e0314bf0706f279c729f51e017"),
		ParentHash: common.StringToHash("0xef1552a40b7165c3cd773806b9e0c165b75356e0314bf0706f279c729f51e017"),
		DAppMainRoot: common.StringToHash("0xef1552a40b7165c3cd773806b9e0c165b75356e0314bf0706f279c729f51e017"),
		MixDigest: common.StringToHash("0xbd4472abb6659ebe3ee06ee4d7b72a00a9f4d001caca51342001075469aff498"),
		Time: big.NewInt(1426516743),
		Nonce: EncodeNonce(uint64(0xa13a5a8c8f2bb1c4)),
	}, transactions: Transactions {
		NewDAppTransaction(
			&dappId,
			3,
			common.HexToAddress("b94f5374fce5edbc8e2a8697c15331677e6ebf0b"),
			big.NewInt(10),
			2000,
			big.NewInt(1),
			common.FromHex("5544"),
		),
		NewDAppTransaction(
			&dappId,
			3,
			common.HexToAddress("b94f5374fce5edbc8e2a8697c15331677e6ebf0b"),
			big.NewInt(10),
			2000,
			big.NewInt(1),
			common.FromHex("5544"),
		)},
	};
	blockEnc, err := rlp.EncodeToBytes(&block)
	if err != nil {
		t.Fatal("encode error: ", err)
	}

	fmt.Print(common.Bytes2Hex(blockEnc) + "\n")
	var decodedBlock Block;
	if err := rlp.DecodeBytes(blockEnc, &decodedBlock); err != nil {
		t.Fatal("decode error: ", err)
	}
	decodedBlock.Print()
	check := func(f string, got, want interface{}) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s mismatch: got %v, want %v", f, got, want)
		}
	}
	check("Root", block.Root(), decodedBlock.Root())
	check("Number", block.Number(), decodedBlock.Number())
	check("DAppID", block.DAppID(), decodedBlock.DAppID())
	check("ParentHash", block.ParentHash(), decodedBlock.ParentHash())
}