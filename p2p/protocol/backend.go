// Copyright 2018 The go-infinet Authors
// This file is part of the go-infinet library.
//
// The go-infinet library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-infinet library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-infinet library. If not, see <http://www.gnu.org/licenses/>.

// Package eth implements the Infinet protocol.
package protocol

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"github.com/juchain/go-juchain/core/account"
	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/common/hexutil"
	"github.com/juchain/go-juchain/consensus"
	"github.com/juchain/go-juchain/consensus/dpos"
	"github.com/juchain/go-juchain/core"
	"github.com/juchain/go-juchain/core/bloombits"
	"github.com/juchain/go-juchain/core/types"
	"github.com/juchain/go-juchain/vm/solc"
	"github.com/juchain/go-juchain/p2p/protocol/downloader"
	"github.com/juchain/go-juchain/p2p/protocol/gasprice"
	"github.com/juchain/go-juchain/p2p/protocol/filters"
	"github.com/juchain/go-juchain/core/store"
	"github.com/juchain/go-juchain/common/event"
	"github.com/juchain/go-juchain/common/log"
	"github.com/juchain/go-juchain/p2p/node"
	"github.com/juchain/go-juchain/p2p"
	"github.com/juchain/go-juchain/config"
	"github.com/juchain/go-juchain/common/rlp"
	"github.com/juchain/go-juchain/rpc"
)

type LesServer interface {
	Start(srvr *p2p.Server)
	Stop()
	Protocols() []p2p.Protocol
	SetBloomBitsIndexer(bbIndexer *core.ChainIndexer)
}

// Juchain implements the Juchain full node service.
type JuchainService struct {
	config      *Config
	chainConfig *config.ChainConfig
	dappConfig  *config.DAppAddress

	// Channel for shutting down the service
	shutdownChan  chan bool    // Channel for shutting down the Juchain
	stopDbUpgrade func() error // stop chain store sequential key upgrade

	// Handlers
	txPool          *core.TxPool
	blockchain      *core.BlockChain
	dappchains      map[common.Address]*core.BlockChain

	protocolManager *ProtocolManager

	// DB interfaces
	chainDb         store.Database // Block chain database
	dappChainDb     map[common.Address]store.Database

	eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *account.Manager

	bloomRequests chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer  *core.ChainIndexer             // Bloom indexer operating during block imports

	ApiBackend *EthApiBackend

	gasPrice  *big.Int
	etherbase common.Address

	networkId     uint64
	netRPCService *p2p.PublicNetAPI

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)
}

// New creates a new Juchain object (including the
// initialisation of the common Juchain object)
func New(ctx *node.ServiceContext, config0 *Config) (*JuchainService, error) {
	if config0.SyncMode == downloader.LightSync {
		return nil, errors.New("can't run eth.JuchainService in light sync mode, use les.LightEthereum")
	}
	if !config0.SyncMode.IsValid() {
		return nil, fmt.Errorf("invalid sync mode %d", config0.SyncMode)
	}
	chainDb, err := CreateDB(ctx, config0, "chaindata")
	if err != nil {
		return nil, err
	}

	stopDbUpgrade := upgradeDeduplicateData(chainDb)
	chainConfig, genesisHash, genesisErr := core.SetupGenesisBlock(chainDb, config0.Genesis)
	if _, ok := genesisErr.(*config.ConfigCompatError); genesisErr != nil && !ok {
		return nil, genesisErr
	}
	log.Info("Initialised chain configuration", "config0", chainConfig)

	eth := &JuchainService{
		config:         config0,
		chainDb:        chainDb,
		chainConfig:    chainConfig,
		dappConfig:     config.DAppAddresses,
		eventMux:       ctx.EventMux,
		accountManager: ctx.AccountManager,
		engine:         CreateConsensusEngine(ctx, chainConfig, chainDb),
		shutdownChan:   make(chan bool),
		stopDbUpgrade:  stopDbUpgrade,
		networkId:      config0.NetworkId,
		gasPrice:       config0.GasPrice,
		etherbase:      config0.Etherbase,
		bloomRequests:  make(chan chan *bloombits.Retrieval),
		bloomIndexer:   NewBloomIndexer(chainDb, config.BloomBitsBlocks),
	}
	for i := range config.DAppAddresses.Addresse {
		dappChainDb, err := CreateDB(ctx, config0, "dappchain"+config.DAppAddresses.Addresse[i].String())
		if err != nil {
			return nil, err
		}
		config0.Genesis.MustCommit(dappChainDb) // bind genesis with DB.
		eth.dappChainDb[config.DAppAddresses.Addresse[i]] = dappChainDb;
	}

	log.Info("Initializing Blockchain protocol", "versions", ProtocolVersions, "network", config0.NetworkId)

	if !config0.SkipBcVersionCheck {
		bcVersion := core.GetBlockChainVersion(chainDb)
		if bcVersion != core.BlockChainVersion && bcVersion != 0 {
			return nil, fmt.Errorf("Blockchain DB version mismatch (%d / %d). Run cmd upgradedb.\n", bcVersion, core.BlockChainVersion)
		}
		core.WriteBlockChainVersion(chainDb, core.BlockChainVersion)
	}
	var (
		vmConfig    = vm.Config{EnablePreimageRecording: config0.EnablePreimageRecording}
		cacheConfig = &core.CacheConfig{Disabled: config0.NoPruning, TrieNodeLimit: config0.TrieCache, TrieTimeLimit: config0.TrieTimeout}
	)
	eth.blockchain, err = core.NewBlockChain(chainDb, cacheConfig, eth.chainConfig, eth.engine, vmConfig)
	if err != nil {
		return nil, err
	}
	for key, dappChainDB := range eth.dappChainDb {
		eth.dappchains[key], err = core.NewBlockChain(dappChainDB, cacheConfig, eth.chainConfig, eth.engine, vmConfig)
		if err != nil {
			log.Error("Fail to instantiate DAppChainDB!", "dapp id", key)
			return nil, err
		}
	}
	// Rewind the chain in case of an incompatible config0 upgrade.
	if compat, ok := genesisErr.(*config.ConfigCompatError); ok {
		log.Warn("Rewinding chain to upgrade configuration", "err", compat)
		eth.blockchain.SetHead(compat.RewindTo)
		core.WriteChainConfig(chainDb, genesisHash, chainConfig)
	}
	eth.bloomIndexer.Start(eth.blockchain)

	if config0.TxPool.Journal != "" {
		config0.TxPool.Journal = ctx.ResolvePath(config0.TxPool.Journal)
	}
	eth.txPool = core.NewTxPool(config0.TxPool, eth.chainConfig, eth.blockchain, eth.dappchains)
	if eth.protocolManager, err = NewProtocolManager(eth, eth.chainConfig, ctx.Config, config0.SyncMode, config0.NetworkId, eth.eventMux, eth.txPool, eth.engine, eth.blockchain, chainDb); err != nil {
		return nil, err
	}

	eth.ApiBackend = &EthApiBackend{eth, nil}
	gpoconfig := config0.GPO
	if gpoconfig.Default == nil {
		gpoconfig.Default = config0.GasPrice
	}
	eth.ApiBackend.gpo = gasprice.NewOracle(eth.ApiBackend, gpoconfig)

	return eth, nil
}

func makeExtraData(extra []byte) []byte {
	if len(extra) == 0 {
		// create default extradata
		extra, _ = rlp.EncodeToBytes([]interface{}{
			uint(config.VersionMajor<<16 | config.VersionMinor<<8 | config.VersionPatch),
			"geth",
			runtime.Version(),
			runtime.GOOS,
		})
	}
	if uint64(len(extra)) > config.MaximumExtraDataSize {
		log.Warn("Miner extra data exceed limit", "extra", hexutil.Bytes(extra), "limit", config.MaximumExtraDataSize)
		extra = nil
	}
	return extra
}

// CreateDB creates the chain database.
func CreateDB(ctx *node.ServiceContext, config *Config, name string) (store.Database, error) {
	db, err := ctx.OpenDatabase(name, config.DatabaseCache, config.DatabaseHandles)
	if err != nil {
		return nil, err
	}
	if db, ok := db.(*store.LDBDatabase); ok {
		db.Meter("node/store/" + name)
	}
	return db, nil
}

// CreateConsensusEngine creates the required type of consensus engine instance for an JuchainService service
func CreateConsensusEngine(ctx *node.ServiceContext, chainConfig *config.ChainConfig, db store.Database) consensus.Engine {
	// If proof-of-authority is requested, set it up
	if (chainConfig.DPoS == nil) {
		chainConfig.DPoS = &config.DPoSConfig{};
	}
	return dpos.New(chainConfig.DPoS, db)
}

// APIs returns the collection of RPC services the ethereum package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *JuchainService) APIs() []rpc.API {
	apis := p2p.GetAPIs(s.ApiBackend)

	// Append any APIs exposed explicitly by the consensus engine
	apis = append(apis, s.engine.APIs(s.BlockChain())...)

	// Append all the local APIs and return
	return append(apis, []rpc.API{
		{
			Namespace: "block",
			Version:   "1.0",
			Service:   NewPublicEthereumAPI(s),
			Public:    true,
		}, {
			Namespace: "block",
			Version:   "1.0",
			Service:   downloader.NewPublicDownloaderAPI(s.protocolManager.downloader, s.eventMux),
			Public:    true,
		}, {
			Namespace: "block",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.ApiBackend, false),
			Public:    true,
		}, {
			Namespace: "admin",
			Version:   "1.0",
			Service:   NewPrivateAdminAPI(s),
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(s),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(s.chainConfig, s),
		}, {
			Namespace: "net",
			Version:   "1.0",
			Service:   s.netRPCService,
			Public:    true,
		},
	}...)
}

func (s *JuchainService) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *JuchainService) Etherbase() (eb common.Address, err error) {
	s.lock.RLock()
	etherbase := s.etherbase
	s.lock.RUnlock()

	if etherbase != (common.Address{}) {
		return etherbase, nil
	}
	if wallets := s.AccountManager().Wallets(); len(wallets) > 0 {
		if accounts := wallets[0].Accounts(); len(accounts) > 0 {
			etherbase := accounts[0].Address

			s.lock.Lock()
			s.etherbase = etherbase
			s.lock.Unlock()

			log.Info("Etherbase automatically configured", "address", etherbase)
			return etherbase, nil
		}
	}
	return common.Address{}, fmt.Errorf("etherbase must be explicitly specified")
}

// set in js console via admin interface or wrapper from cli flags
func (self *JuchainService) SetEtherbase(etherbase common.Address) {
	self.lock.Lock()
	self.etherbase = etherbase
	self.lock.Unlock()

	//self.miner.SetEtherbase(etherbase)
}

func (s *JuchainService) AccountManager() *account.Manager   { return s.accountManager }
func (s *JuchainService) BlockChain() *core.BlockChain       { return s.blockchain }
func (s *JuchainService) TxPool() *core.TxPool               { return s.txPool }
func (s *JuchainService) EventMux() *event.TypeMux           { return s.eventMux }
func (s *JuchainService) Engine() consensus.Engine           { return s.engine }
func (s *JuchainService) ChainDb() store.Database            { return s.chainDb }
func (s *JuchainService) IsListening() bool                  { return true } // Always listening
func (s *JuchainService) EthVersion() int                    { return int(s.protocolManager.SubProtocols[0].Version) }
func (s *JuchainService) NetVersion() uint64                 { return s.networkId }
func (s *JuchainService) Downloader() *downloader.Downloader { return s.protocolManager.downloader }

// Protocols implements node.Service, returning all the currently configured
// network protocols to start.
func (s *JuchainService) Protocols() []p2p.Protocol {
	return s.protocolManager.SubProtocols
}

// Start implements node.Service, starting all internal goroutines needed by the
// JuchainService protocol implementation.
func (s *JuchainService) Start(srvr *p2p.Server) error {
	// Start the bloom bits servicing goroutines
	s.startBloomHandlers()

	// Start the RPC service
	s.netRPCService = p2p.NewPublicNetAPI(srvr, s.NetVersion())

	// Figure out a max peers count based on the server limits
	maxPeers := srvr.MaxPeers
	if s.config.LightServ > 0 {
		if s.config.LightPeers >= srvr.MaxPeers {
			return fmt.Errorf("invalid peer config: light peer count (%d) >= total peer count (%d)", s.config.LightPeers, srvr.MaxPeers)
		}
		maxPeers -= s.config.LightPeers
	}
	// Start the networking layer and the light server if requested
	s.protocolManager.Start(maxPeers)

	return nil
}

// Stop implements node.Service, terminating all internal goroutines used by the
// JuchainService protocol.
func (s *JuchainService) Stop() error {
	if s.stopDbUpgrade != nil {
		s.stopDbUpgrade()
	}
	s.bloomIndexer.Close()
	s.blockchain.Stop()
	s.protocolManager.Stop()

	s.txPool.Stop()
	s.eventMux.Stop()

	s.chainDb.Close()
	close(s.shutdownChan)

	return nil
}
