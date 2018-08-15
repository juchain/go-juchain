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
	"fmt"
	"reflect"
)

// DPoS consensus handler of delegator packaging process.
// only 31 delegators voted, then this process will be started.
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

	VotingAccessor  DelegatorAccessor; // responsible for access voting data.
	DelegatorsTable   []string;         // only for all delegated node ids. the table will receive from a voting contract.
	DelegatorNodeInfo []*discover.Node; // all delegated peers. = make([]*discover.Node, 0, len(urls))

)

// Delegator table refers to the voting contract.
type DelegatorAccessor interface {
	Refresh() (delegatorsTable []string, delegatorNodes []*discover.Node)
}

// only for test purpose.
type DelegatorAccessorTestImpl struct {
	currNodeId           string;           // current short node id.
	currNodeIdHash       []byte;           // short node id hash.
}
func (d *DelegatorAccessorTestImpl) Refresh() (delegatorsTable []string, delegatorNodes []*discover.Node) {
	return []string{d.currNodeId}, []*discover.Node{}
}

type DPoSProtocolManager struct {
	networkId     uint64;
	eth           *JuchainService;
	ethManager    *ProtocolManager;
	blockchain    *core.BlockChain;

	lock          *sync.Mutex; // protects running
	packager      *dpos.Packager;
	t1            *time.Timer; // global synchronized timer.
}

// NewProtocolManager returns a new obod sub protocol manager. The JuchainService sub protocol manages peers capable
// with the obod network.
func NewDPoSProtocolManager(eth *JuchainService, ethManager *ProtocolManager, config *config.ChainConfig, config2 *node.Config,
	mode downloader.SyncMode, networkId uint64, blockchain *core.BlockChain, engine consensus.Engine) (*DPoSProtocolManager) {
	// Create the protocol manager with the base fields
	manager := &DPoSProtocolManager{
		networkId:         networkId,
		eth:               eth,
		ethManager:        ethManager,
		blockchain:        blockchain,
		lock:              &sync.Mutex{},
		packager:          dpos.NewPackager(config, engine, DefaultConfig.Etherbase, eth, eth.EventMux()),
	}
	currNodeId = discover.PubkeyID(&config2.NodeKey().PublicKey).TerminalString();
	currNodeIdHash = common.Hex2Bytes(currNodeId);
	if TestMode {
		VotingAccessor = &DelegatorAccessorTestImpl{currNodeId:currNodeId, currNodeIdHash:currNodeIdHash};
	}
	return manager
}

func (pm *DPoSProtocolManager) Start() {
	if TestMode {
		DelegatorsTable = []string{currNodeId}
	}
	if pm.isDelegatedNode() {
		log.Info("I am a delegator.")
		pm.packager.Start();
		go pm.schedule();
		if !TestMode {
			time.AfterFunc(time.Second*time.Duration(SmallPeriodInterval), pm.syncDelegatedNodeSafely) //initial attempt.
		}
	}
}

func (pm *DPoSProtocolManager) schedule() {
	t2 := time.NewTimer(time.Second * time.Duration(1))
	for {
		select {
		case <-t2.C:
			go pm.roundRobinSafely();
			t2 = time.NewTimer(time.Second * time.Duration(1))
		}
	}
}

// this is a loop function for electing node.
func (pm *DPoSProtocolManager) syncDelegatedNodeSafely() {
	if !pm.isDelegatedNode() {
		// only candidate node is able to participant to this process.
		return;
	}
	log.Info("Preparing for next big period...");
	// pull the newest delegators from voting contract.
	DelegatorsTable, DelegatorNodeInfo = VotingAccessor.Refresh()
	if uint8(len(GigPeriodHistory)) >= BigPeriodHistorySize {
		GigPeriodHistory = GigPeriodHistory[1:] //remove the first old one.
	}
	if len(DelegatorsTable) == 0 || pm.ethManager.peers.Len() == 0 {
		log.Info("Sorry, could not detect any delegator!");
		return;
	}
	round := uint64(1)
	activeTime := uint64(time.Now().Unix() + int64(GigPeriodInterval))
	if NextGigPeriodInstance != nil {
		if !TestMode {
			gap := int64(NextGigPeriodInstance.activeTime) - time.Now().Unix()
			if gap > 2 || gap < -2 {
				log.Warn(fmt.Sprintf("Scheduling of the new electing round is improper! current gap: %v seconds", gap))
				//restart the scheduler
				NextElectionInfo = nil;
				pm.syncDelegatedNodeSafely();
				return;
			}
		}
		round = NextGigPeriodInstance.round + 1
		activeTime = GigPeriodInstance.activeTime + uint64(GigPeriodInterval)
		// keep the big period history for block validation.
		GigPeriodHistory[len(GigPeriodHistory)-1] = *NextGigPeriodInstance;

		GigPeriodInstance = &GigPeriodTable{
			NextGigPeriodInstance.round,
			NextGigPeriodInstance.state,
			NextGigPeriodInstance.delegatedNodes,
			NextGigPeriodInstance.delegatedNodesSign,
			NextGigPeriodInstance.confirmedTickets,
			NextGigPeriodInstance.confirmedBestNode,
			NextGigPeriodInstance.activeTime,
		};
		log.Info(fmt.Sprintf("Switched the new big period round. %d ", GigPeriodInstance.round));
	}

	// make sure all delegators are synced at this round.
	NextGigPeriodInstance = &GigPeriodTable{
		round,
		STATE_LOOKING,
		DelegatorsTable,
		SignCandidates(DelegatorsTable),
		make(map[string]uint32),
		make(map[string]*GigPeriodTable),
		activeTime,
	};
	pm.trySyncAllDelegators()
}
func (pm *DPoSProtocolManager) trySyncAllDelegators() {
	if TestMode {
		return;
	}
	//send this round to all delegated peers.
	//all delegated must giving the response in SYNC_BIGPERIOD_RESPONSE state.
	for _, delegator := range NextGigPeriodInstance.delegatedNodes {
		// make sure all delegator are alive.
		if pm.ethManager.peers.Peer(delegator) == nil {
			// try to add DelegatorNodeInfo[i] into peers table.
			// but can't talk to it directly.
			for i,e := range DelegatorsTable {
				if e == delegator {
					pm.eth.server.AddPeer(DelegatorNodeInfo[i]);
					break;
				}
			}
		} else {
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
			log.Info("I am not ready!!!")
			return nil;
		}
		if request.Round == NextGigPeriodInstance.round {
			if NextGigPeriodInstance.state == STATE_CONFIRMED {
				log.Debug(fmt.Sprintf("I am in the agreed round %v", NextGigPeriodInstance.round));
				// if i have already confirmed this round. send this round to peer.
				if TestMode {
					return nil;
				}
				return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
					NextGigPeriodInstance.round,
					NextGigPeriodInstance.activeTime,
					NextGigPeriodInstance.delegatedNodes,
					NextGigPeriodInstance.delegatedNodesSign,
					STATE_CONFIRMED,
					currNodeIdHash});
			} else {
				if !reflect.DeepEqual(DelegatorsTable, request.DelegatedTable) {
					if len(DelegatorsTable) < len(request.DelegatedTable) {
						// refresh table if mismatch.
						DelegatorsTable, DelegatorNodeInfo = VotingAccessor.Refresh()
					}
					if !reflect.DeepEqual(DelegatorsTable, request.DelegatedTable) {
						log.Debug("Delegators are mismatched in two tables.");
						if TestMode {
							return nil;
						}
						// both delegators are not matched,  both lose the election power of this round.
						return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
							NextGigPeriodInstance.round,
							NextGigPeriodInstance.activeTime,
							NextGigPeriodInstance.delegatedNodes,
							NextGigPeriodInstance.delegatedNodesSign,
							STATE_MISMATCHED_DNUMBER,
							currNodeIdHash});
					}
				}
				NextGigPeriodInstance.state = STATE_CONFIRMED;
				NextGigPeriodInstance.delegatedNodes = request.DelegatedTable;
				NextGigPeriodInstance.delegatedNodesSign = request.DelegatedTableSign;
				NextGigPeriodInstance.activeTime = request.ActiveTime;

				pm.setNextRoundTimer();//sync the timer.
				log.Debug(fmt.Sprintf("Agreed this table %v as %v round", NextGigPeriodInstance.delegatedNodes, NextGigPeriodInstance.round));
				if TestMode {
					return nil;
				}
				// broadcast it to all peers again.
				for _, peer := range pm.ethManager.peers.peers {
					err := peer.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
						NextGigPeriodInstance.round,
						NextGigPeriodInstance.activeTime,
						NextGigPeriodInstance.delegatedNodes,
						NextGigPeriodInstance.delegatedNodesSign,
						STATE_CONFIRMED,
						currNodeIdHash})
					if (err != nil) {
						log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
					}
				}
			}
		} else if request.Round < NextGigPeriodInstance.round {
			log.Debug(fmt.Sprintf("Mismatched request.round %v, CurrRound %v: ", request.Round, NextGigPeriodInstance.round))
			if TestMode {
				return nil;
			}
			return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
				NextGigPeriodInstance.round,
				NextGigPeriodInstance.activeTime,
				NextGigPeriodInstance.delegatedNodes,
				NextGigPeriodInstance.delegatedNodesSign,
				STATE_MISMATCHED_ROUND,
				currNodeIdHash});
		} else if request.Round > NextGigPeriodInstance.round {
			if NextGigPeriodInstance.state == STATE_CONFIRMED {
				if TestMode {
					return nil;
				}
				return p.SendSyncBigPeriodResponse(&SyncBigPeriodResponse{
					NextGigPeriodInstance.round,
					NextGigPeriodInstance.activeTime,
					NextGigPeriodInstance.delegatedNodes,
					NextGigPeriodInstance.delegatedNodesSign,
					STATE_CONFIRMED,
					currNodeIdHash});
			}
			log.Debug(fmt.Sprintf("Mismatched request.round %v, CurrRound %v ", request.Round, NextGigPeriodInstance.round))
			// skip the conditions.
		}
	case msg.Code == SYNC_BIGPERIOD_RESPONSE:
		var response SyncBigPeriodResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if response.Round != NextGigPeriodInstance.round {
			return nil;
		}
		if SignCandidates(response.DelegatedTable) != response.DelegatedTableSign {
			return errResp(DPOSErroDelegatorSign, "");
		}
		nodeId := common.Bytes2Hex(response.NodeId)
		log.Debug("Received SYNC Big Period response: " + nodeId);
		NextGigPeriodInstance.confirmedTickets[nodeId] ++;
		NextGigPeriodInstance.confirmedBestNode[nodeId] = &GigPeriodTable{
			response.Round,
			STATE_CONFIRMED,
			response.DelegatedTable,
			response.DelegatedTableSign,
			nil,
			nil,
			response.ActiveTime,
		};

		maxTickets, bestNodeId := uint32(0), "";
		for key, value := range NextGigPeriodInstance.confirmedTickets {
			if maxTickets < value {
				maxTickets = value;
				bestNodeId = key;
			}
		}
		if NextGigPeriodInstance.state == STATE_CONFIRMED {
			// set the best node as the final state.
			bestNode := NextGigPeriodInstance.confirmedBestNode[bestNodeId];
			NextGigPeriodInstance.delegatedNodes = bestNode.delegatedNodes;
			NextGigPeriodInstance.delegatedNodesSign = bestNode.delegatedNodesSign;
			NextGigPeriodInstance.activeTime = bestNode.activeTime;
			log.Debug(fmt.Sprintf("Updated the best table: %v", bestNode.delegatedNodes));
			pm.setNextRoundTimer();
		} else if NextGigPeriodInstance.state == STATE_LOOKING && uint32(NextGigPeriodInstance.confirmedTickets[bestNodeId]) > uint32(len(NextGigPeriodInstance.delegatedNodes)) {
			NextGigPeriodInstance.state = STATE_CONFIRMED;
			NextGigPeriodInstance.delegatedNodes = response.DelegatedTable;
			NextGigPeriodInstance.delegatedNodesSign = response.DelegatedTableSign;
			NextGigPeriodInstance.activeTime = response.ActiveTime;

			pm.setNextRoundTimer();
		} else if response.State == STATE_MISMATCHED_ROUND {
			// force to create new round
			NextGigPeriodInstance = &GigPeriodTable{
				response.Round,
				STATE_LOOKING,
				response.DelegatedTable,
				response.DelegatedTableSign,
				make(map[string]uint32),
				make(map[string]*GigPeriodTable),
				response.ActiveTime,
			};
			pm.trySyncAllDelegators()
		} else if response.State == STATE_MISMATCHED_DNUMBER {
			// refresh table only, and this node loses the election power of this round.
			DelegatorsTable, DelegatorNodeInfo = VotingAccessor.Refresh()
		}
		return nil;
	default:
		return errResp(ErrInvalidMsgCode, "%v", msg.Code)
	}
	return nil
}

func (pm *DPoSProtocolManager) setNextRoundTimer() {
	leftTime := int64(NextGigPeriodInstance.activeTime) - time.Now().Unix()
	if leftTime < 1 {
		log.Warn("Discard this round due to the expiration of the active time.")
		return;
	}
	if pm.t1 != nil {
		// potentially could be an issue if the timer is unable to be cancelled.
		if pm.t1.Stop() {
			pm.t1 = time.AfterFunc(time.Second*time.Duration(leftTime), pm.syncDelegatedNodeSafely)
		}
	} else {
		pm.t1 = time.AfterFunc(time.Second*time.Duration(leftTime), pm.syncDelegatedNodeSafely)
	}
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
		round := self.blockchain.CurrentFastBlock().Header().Round;
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
	confirmedTickets   map[string]uint32;   // 31 node must be confirmed this ticket or must equal to delegatedNodes length.
	confirmedBestNode  map[string]*GigPeriodTable;  // confirmed the next active time from all peers. <nodeid><GigPeriodTable>
	activeTime         uint64;       // Unix timestamp for all nodes.
}
func (t *GigPeriodTable) wasHisTurn(round uint64, nodeId string, minedTime int64) bool {
	for i :=0; i < len(t.delegatedNodes); i++ {
		if t.delegatedNodes[i] == nodeId {
			beatStartTime := int64(t.activeTime) + (int64(i) * int64(SmallPeriodInterval))
			if beatStartTime <= minedTime && (beatStartTime+ int64(SmallPeriodInterval)) >= minedTime {
				return true;
			}
		}
	}
	// check the history.
	if len(GigPeriodHistory) > 0 {
		for _, v := range GigPeriodHistory {
			if int64(v.activeTime) <= minedTime && (int64(v.activeTime) + int64(SmallPeriodInterval)) >= minedTime {
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
			beatStartTime := int64(t.activeTime) + (int64(i) * int64(SmallPeriodInterval))
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
		beatStartTime := int64(t.activeTime) + (int64(i) * int64(SmallPeriodInterval))
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
	return common.BytesToHash(hw.Sum(signCandidates))
}