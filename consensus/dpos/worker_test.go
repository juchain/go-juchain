package dpos

import (
	"testing"
	"github.com/juchain/go-juchain/config"
	"math/big"
	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/p2p/protocol"

)

func TestGenerateNewBlock(t *testing.T) {

	engine := New(&config.DPoSConfig{0,0}, nil);
	eth := &protocol.JuchainService{
	}
	packager := NewPackager(&config.ChainConfig{big.NewInt(1337), big.NewInt(0) , nil, nil, new(config.DPoSConfig)}, engine, common.Address{},eth, eth.EventMux());

	packager.Start();

	packager.GenerateNewBlock();
	packager.GenerateNewBlock();

	packager.Stop();
}
