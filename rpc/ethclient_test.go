// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package rpc

import (
	"github.com/juchain/go-juchain"
)

// Verify that EthClient implements the Juchain interfaces.
var (
	_ = juchain.ChainReader(&EthClient{})
	_ = juchain.TransactionReader(&EthClient{})
	_ = juchain.ChainStateReader(&EthClient{})
	_ = juchain.ChainSyncReader(&EthClient{})
	_ = juchain.ContractCaller(&EthClient{})
	_ = juchain.GasEstimator(&EthClient{})
	_ = juchain.GasPricer(&EthClient{})
	_ = juchain.LogFilterer(&EthClient{})
	_ = juchain.PendingStateReader(&EthClient{})
	// _ = juchain.PendingStateEventer(&EthClient{})
	_ = juchain.PendingContractCaller(&EthClient{})
)
