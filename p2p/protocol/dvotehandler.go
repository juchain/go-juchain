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


package protocol

import (
	"time"
	"strconv"
	"sync"
	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/common/rlp"
	"github.com/juchain/go-juchain/common/crypto/sha3"
	"github.com/juchain/go-juchain/common/log"
	"github.com/juchain/go-juchain/core"
	"github.com/juchain/go-juchain/p2p/protocol/downloader"
	"github.com/juchain/go-juchain/p2p"
	"github.com/juchain/go-juchain/p2p/discover"
	"github.com/juchain/go-juchain/p2p/node"
	"github.com/juchain/go-juchain/consensus"
	"github.com/juchain/go-juchain/consensus/dpos"
	"github.com/juchain/go-juchain/config"
	"reflect"
	"fmt"
)

// DPoS packaging handler.
/**
   Sample code:
   for round i
   dlist_i = get N delegates sort by votes
   dlist_i = mixorder(dlist_i)
   loop
       slot = global_time_offset / block_interval
       pos = slot % N
       if dlist_i[pos] exists in this node
           generateBlock(keypair of dlist_i[pos])
       else
           skip
 */
var (
	currNodeId           string;           // current short node id.
	currNodeIdHash       []byte;           // short node id hash.
	TotalDelegatorNumber uint8  = 31;                               // we make 31 candidates as the best group for packaging.
	SmallPeriodInterval  uint32 = 5;                                // small period for packaging node in every 5 seconds.
	GigPeriodInterval    uint32 = uint32(TotalDelegatorNumber) * 5; // create a big period for all delegated nodes in every 155 seconds.

	BigPeriodHistorySize  uint8 = 10; // keep 10 records for the confirmation of delayed block
	GigPeriodHistory      = make([]GigPeriodTable, 0); // <GigPeriodTable>
	GigPeriodInstance     *GigPeriodTable; // we use two versions of election info for switching delegated nodes smoothly.
	NextGigPeriodInstance *GigPeriodTable;

	DelegatorsTable   []string;         // only for all delegated node ids. the table will receive from a voting contract.
	DelegatorNodeInfo []*discover.Node; // all delegated peers. = make([]*discover.Node, 0, len(urls))

	blockchainRef         *core.BlockChain;
)

// Delegator table refers to the voting contract.
type DelegatorVotingManager interface {
	Refresh() (DelegatorsTable []string, DelegatorNodes []*discover.Node)
}

type DPoSProtocolManager struct {
	networkId     uint64;
	ethManager    *ProtocolManager;
	blockchain    *core.BlockChain;

	// channels for fetcher, syncer, txsyncLoop
	newPeerCh     chan *peer;
	lock          *sync.Mutex; // protects running

	packager       *dpos.Packager;
	votingManager  *DelegatorVotingManager;
}

// NewProtocolManager returns a new ethereum sub protocol manager. The JuchainService sub protocol manages peers capable
// with the ethereum network.
func NewDPoSProtocolManager(eth *JuchainService, ethManager *ProtocolManager, config *config.ChainConfig, config2 *node.Config,
	mode downloader.SyncMode, networkId uint64, blockchain *core.BlockChain, engine consensus.Engine) (*DPoSProtocolManager) {
	// Create the protocol manager with the base fields
	blockchainRef = blockchain;
	manager := &DPoSProtocolManager{
		networkId:         networkId,
		ethManager:        ethManager,
		blockchain:        blockchain,
		newPeerCh:         make(chan *peer),
		lock:              &sync.Mutex{},
		packager:          dpos.NewPackager(config, engine, DefaultConfig.Etherbase, eth, eth.EventMux()),
	}
	currNodeId = discover.PubkeyID(&config2.NodeKey().PublicKey).TerminalString();
	currNodeIdHash = common.Hex2Bytes(currNodeId);

	return manager
}

func (pm *DPoSProtocolManager) Start(maxPeers int) {
	log.Info("Starting DPoS Consensus")

	if pm.isDelegatedNode() {
		pm.packager.Start();
		go pm.syncDelegatedNodeSafely();
		go pm.roundRobinSafely();
		time.AfterFunc(time.Second*time.Duration(GigPeriodInterval), pm.scheduleSyncBigPeriod);
		time.AfterFunc(time.Second*time.Duration(1), pm.scheduleSmallPeriod);;
	}
}

func (pm *DPoSProtocolManager) scheduleSmallPeriod() {
	pm.roundRobinSafely();
	time.AfterFunc(time.Second*time.Duration(1), pm.scheduleSmallPeriod);
}

func (pm *DPoSProtocolManager) scheduleSyncBigPeriod() {
	pm.syncDelegatedNodeSafely();
	time.AfterFunc(time.Second*time.Duration(GigPeriodInterval), pm.scheduleSyncBigPeriod);
}

// this is a loop function for electing node.
func (pm *DPoSProtocolManager) syncDelegatedNodeSafely() {
	if !pm.isDelegatedNode() {
		// only candidate node is able to participant to this process.
		return;
	}
	log.Info("Preparing for next big period...");
	// TODO: pull the newest delegators from voting contract.
	// DelegatorsTable, DelegatorNodeInfo = pm.votingManager.Refresh()
	if uint8(len(GigPeriodHistory)) >= BigPeriodHistorySize {
		GigPeriodHistory = GigPeriodHistory[1:] //remove the first old one.
	}
	if len(DelegatorsTable) == 0 || pm.ethManager.peers.Len() == 0 {
		log.Info("Sorry, could not detect any delegator found!");
		return;
	}
	if NextGigPeriodInstance != nil {
		// keep the big period history for block validation.
		GigPeriodHistory[len(GigPeriodHistory)-1] = *NextGigPeriodInstance;
	}
	round := uint64(1)
	if NextGigPeriodInstance != nil {
		round = NextGigPeriodInstance.round + 1
	}
	activeTime := time.Now().Unix() + int64(GigPeriodInterval)
	if GigPeriodInstance != nil {
		activeTime = GigPeriodInstance.activeTime + int64(GigPeriodInterval)
	}
	// make sure all delegators are synced at this round.
	NextGigPeriodInstance = &GigPeriodTable{
		round,
		STATE_LOOKING,
		DelegatorsTable,
		SignCandidates(DelegatorsTable),
		0,
		activeTime,
	};
	pm.trySyncAllDelegators()
}
func (pm *DPoSProtocolManager) trySyncAllDelegators() {
	//send this round to all delegated peers.
	//all delegated must giving the response in SYNC_BIGPERIOD_RESPONSE state.
	for _, delegator := range NextGigPeriodInstance.delegatedNodes {
		if pm.ethManager.peers.Peer(delegator) == nil {
			//todo: add DelegatorNodeInfo[i] into peers table.
		}
		err := pm.ethManager.peers.Peer(delegator).SendSyncBigPeriodRequest(
			&SyncBigPeriodRequest{NextGigPeriodInstance.round,
				NextGigPeriodInstance.activeTime,
				NextGigPeriodInstance.delegatedNodes,
				NextGigPeriodInstance.delegatedNodesSign,
				currNodeIdHash});
		if err != nil {
			log.Debug("Error occurred while sending SyncBigPeriodRequest: " + err.Error())
		}
	}
}

// handleMsg is invoked whenever an inbound message is received from a remote
// peer. The remote connection is torn down upon returning any error.
func (pm *DPoSProtocolManager) handleMsg(msg *p2p.Msg, p *peer) error {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	// Handle the message depending on its contents
	switch {
	case msg.Code == SYNC_BIGPERIOD_REQUEST:
		var request SyncBigPeriodRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if SignCandidates(request.DelegatedTable) != request.DelegatedTableSign {
			return errResp(DPOSErroDelegatorSign, "");
		}
		if DelegatorsTable == nil || len(DelegatorsTable) == 0 {
			// i am not ready.
			return nil;
		}
		if NextGigPeriodInstance.state == STATE_CONFIRMED {
			if request.Round < NextGigPeriodInstance.round {
				return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
					NextGigPeriodInstance.round,
					NextGigPeriodInstance.activeTime,
					NextGigPeriodInstance.delegatedNodes,
					NextGigPeriodInstance.delegatedNodesSign,
					STATE_MISMATCHED_ROUND,
					currNodeIdHash});
			}
			// if i have already confirmed this round. send this round to peer.
			return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
				NextGigPeriodInstance.round,
				NextGigPeriodInstance.activeTime,
				NextGigPeriodInstance.delegatedNodes,
				NextGigPeriodInstance.delegatedNodesSign,
				STATE_CONFIRMED,
				currNodeIdHash});
		} else if NextGigPeriodInstance.state == STATE_LOOKING {
			if request.Round < NextGigPeriodInstance.round{
				return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
					NextGigPeriodInstance.round,
					NextGigPeriodInstance.activeTime,
					NextGigPeriodInstance.delegatedNodes,
					NextGigPeriodInstance.delegatedNodesSign,
					STATE_MISMATCHED_ROUND,
					currNodeIdHash});
			}
			if !reflect.DeepEqual(DelegatorsTable, request.DelegatedTable) {
				if len(DelegatorsTable) < len(request.DelegatedTable) {
					//todo refresh table if mismatch.
				}
				if !reflect.DeepEqual(DelegatorsTable, request.DelegatedTable) {
					// i got more then you, reject your request.
					return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
						NextGigPeriodInstance.round,
						NextGigPeriodInstance.activeTime,
						NextGigPeriodInstance.delegatedNodes,
						NextGigPeriodInstance.delegatedNodesSign,
						STATE_MISMATCHED_DNUMBER,
						currNodeIdHash});
				}
			}
			// even though i have not confirmed this round yet, we met all conditions. send this round to peer.
			return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
				NextGigPeriodInstance.round,
				NextGigPeriodInstance.activeTime,
				NextGigPeriodInstance.delegatedNodes,
				NextGigPeriodInstance.delegatedNodesSign,
				STATE_CONFIRMED,
				currNodeIdHash});
		} else {
			log.Warn("SYNC Process must not going to here!!!!")
		}
	case msg.Code == SYNC_BIGPERIOD_RESPONSE:
		var response SyncBigPeriodResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if NextGigPeriodInstance.state == STATE_CONFIRMED {
			return nil;
		}
		if response.State == STATE_CONFIRMED {
			NextGigPeriodInstance.confirmedTickets++;
		} else if response.State == STATE_MISMATCHED_ROUND && NextGigPeriodInstance.round < response.Round{
			// force to create new round
			NextGigPeriodInstance = &GigPeriodTable{
				response.Round,
				STATE_LOOKING,
				response.DelegatedTable,
				response.DelegatedTableSign,
				0,
				response.activeTime,
			};
			pm.trySyncAllDelegators()
		} else if response.State == STATE_MISMATCHED_DNUMBER {
			//todo: force to refresh table

		}
		// TODO: need a counter to be confirmed from 2/3 nodes.
		if NextGigPeriodInstance.confirmedTickets == uint8(len(NextGigPeriodInstance.delegatedNodes)) {
			NextGigPeriodInstance.state = STATE_CONFIRMED;

			//switch to GigPeriodInstance when the next round begins.
			leftTime := NextGigPeriodInstance.activeTime - time.Now().Unix()
			time.AfterFunc(time.Second*time.Duration(leftTime), func() {
				GigPeriodInstance = &GigPeriodTable{
					NextGigPeriodInstance.round,
					NextGigPeriodInstance.state,
					NextGigPeriodInstance.delegatedNodes,
					NextGigPeriodInstance.delegatedNodesSign,
					NextGigPeriodInstance.confirmedTickets,
					NextGigPeriodInstance.activeTime,
				};
				log.Info(fmt.Sprintf("Switched the new big period round. %d ", GigPeriodInstance.round));
			});
		}
		return nil;
	default:
		return errResp(ErrInvalidMsgCode, "%v", msg.Code)
	}
	return nil
}

// the node would not be a candidate if it is not qualified.
func (pm *DPoSProtocolManager) isDelegatedNode() bool {
	if DelegatorsTable == nil {
		return false;
	}
	for i :=0; i < len(DelegatorsTable); i++ {
		if DelegatorsTable[i] == currNodeId {
			return true;
		}
	}
	return false;
}

func (pm *DPoSProtocolManager) isDelegatedNode2(nodeId string) bool {
	if DelegatorsTable == nil {
		return false;
	}
	for i :=0; i < len(DelegatorsTable); i++ {
		if DelegatorsTable[i] == nodeId {
			return true;
		}
	}
	return false;
}

func (pm *DPoSProtocolManager) Stop() {
	if pm.isDelegatedNode() {
		pm.packager.Stop();
	}
	// Quit the sync loop.
	log.Info("DPoS Consensus stopped")
}

func (pm *DPoSProtocolManager) newPeer(pv uint, p *p2p.Peer, rw p2p.MsgReadWriter) *peer {
	return newPeer(pv, p, newMeteredMsgWriter(rw))
}

// --------------------Packaging Process-------------------//
// start round robin for packaging blocks in small period.
func (self *DPoSProtocolManager) roundRobinSafely() {
	if !self.isDelegatedNode() || GigPeriodInstance == nil {
		return;
	}
	log.Info(GigPeriodInstance.whosTurn())
	// generate block by election node.
	if GigPeriodInstance.isMyTurn() {
		log.Info("it's my turn now " + time.Now().String());
		round := blockchainRef.CurrentFastBlock().Header().Round;
		block := self.packager.GenerateNewBlock(round+1, currNodeId);
		block.ToString();
		//response := &PackageResponse{block.Round(), currNodeId, block.Hash(),DPOSMSG_SUCCESS};
	}
}
// this GigPeriodTable only serves for delegators.
type GigPeriodTable struct {
	round              uint64;       // synchronization round
	state              uint8;       // STATE_LOOKING
	delegatedNodes     []string;    // all 31 nodes id
	delegatedNodesSign common.Hash; // a security sign for all delegated nodes which can be verified from node array.
	confirmedTickets   uint8;       // 31 node must be confirmed this ticket or must equal to delegatedNodes length.
	activeTime         int64;       // Unix timestamp for all nodes.
}
func (t *GigPeriodTable) wasHisTurn(round uint64, nodeId string, minedTime int64) bool {
	for i :=0; i < len(t.delegatedNodes); i++ {
		if t.delegatedNodes[i] == nodeId {
			beatStartTime := t.activeTime + (int64(i) * int64(SmallPeriodInterval))
			if beatStartTime <= minedTime && (beatStartTime+ int64(SmallPeriodInterval)) >= minedTime {
				return true;
			}
		}
	}
	// check the history.
	if len(GigPeriodHistory) > 0 {
		for _, v := range GigPeriodHistory {
			if v.activeTime <= minedTime && (v.activeTime+ int64(SmallPeriodInterval)) >= minedTime {
				for i :=0; i < len(v.delegatedNodes); i++ {
					if v.delegatedNodes[i] == nodeId {
						//todo check round as well.
						return true;
					}
				}
			}
		}
	}
	return false;
}
func (t *GigPeriodTable) isMyTurn() bool {
	for i :=0; i < len(t.delegatedNodes); i++ {
		if t.delegatedNodes[i] == currNodeId {
			beatStartTime := t.activeTime + (int64(i) * int64(SmallPeriodInterval))
			currTime := time.Now().Unix()
			// we only give 4s to avoid the mismatched timestamp issue of last packaging.
			if beatStartTime <= currTime && (beatStartTime+ int64(SmallPeriodInterval)) > currTime {
				return true;
			}
		}
	}
	return false;
}
func (t *GigPeriodTable) whosTurn() string {
	currTime := time.Now().Unix()
	for i :=0; i < len(t.delegatedNodes); i++ {
		beatStartTime := t.activeTime + (int64(i) * int64(SmallPeriodInterval))
		if beatStartTime <= currTime && (beatStartTime+ int64(SmallPeriodInterval)) >= currTime {
			return "Who's turn: {position: " + strconv.Itoa(i) + ", delegator: " + t.delegatedNodes[i] + " }";
		}
	}
	return "";
}
func (t *GigPeriodTable) isDelegatedNode(nodeId string) bool {
	for i :=0; i < len(t.delegatedNodes); i++ {
		if t.delegatedNodes[i] == nodeId {
			return true;
		}
	}
	return false;
}

func RemoveCanditate(s []string, i int) []string {
	s[len(s)-1], s[i] = s[i], s[len(s)-1]
	return s[:len(s)-1]
}

func SignCandidates(candidates []string) common.Hash {
	var signCandidates = []byte{}
	hw := sha3.NewKeccak256()
	rlp.Encode(hw, candidates)
	hw.Sum(signCandidates)
	return common.BytesToHash(signCandidates)
}