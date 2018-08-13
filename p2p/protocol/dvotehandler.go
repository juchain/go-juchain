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
	"github.com/juchain/go-juchain/common/log"
	"github.com/juchain/go-juchain/core"
	"github.com/juchain/go-juchain/p2p/protocol/downloader"
	"github.com/juchain/go-juchain/p2p"
	"github.com/juchain/go-juchain/p2p/discover"
	"github.com/juchain/go-juchain/p2p/node"
	"github.com/juchain/go-juchain/consensus"
	"github.com/juchain/go-juchain/consensus/dpos"
	"github.com/juchain/go-juchain/config"
	"math/rand"
	"fmt"
)

// DPoS voting handler is purposed on the voting process of all delegators.
// this is the previous process of DPoS delegator packaging process.
// we need to vote at least 31 delegators and 70 candidates in the smart contract.
// if all the conditions satisfied, then activate the delegator packaging process.
// DPoS packaging handler.
var (
	TestMode          bool   = false; // only for test case.
	PackagingInterval uint32 = 2;     // vote for packaging node in every 5 seconds.
	ElectingInterval  uint32 = 30;    // elect for new node in every 30 seconds.

	ElectionInfo0    *ElectionInfo; // we use two versions of election info for switching election node smoothly.
	NextElectionInfo *ElectionInfo;
)

// Delegator table refers to the voting contract.
type DelegatorVotingManager interface {
	Refresh() (delegatorsTable []string, delegatorNodes []*discover.Node)
}

type ElectionInfo struct {
	round            uint64;
	enodestate       uint8; //= VOTESTATE_LOOKING
	electionTickets  uint32; // default tickets which counts on all peers.
	confirmedTickets map[string]uint32; // confirmed tickets from all peers. <nodeid><tickets>
	electionNodeId   string; //= ""
	electionNodeIdHash []byte; // the election node id.

	activeTime         int64; // active time of next round.
	latestActiveENode  time.Time;// = time.Now(); // the check whether the election node is active or not.
}

type DVoteProtocolManager struct {
	networkId     uint64;
	ethManager    *ProtocolManager;
	blockchain    *core.BlockChain;

	lock          *sync.Mutex; // protects running

	packager      *dpos.Packager;
	dposManager	  *DPoSProtocolManager;
}

// NewProtocolManager returns a new ethereum sub protocol manager. The JuchainService sub protocol manages peers capable
// with the ethereum network.
func NewDVoteProtocolManager(eth *JuchainService, ethManager *ProtocolManager, config *config.ChainConfig, config2 *node.Config,
	mode downloader.SyncMode, networkId uint64, blockchain *core.BlockChain, engine consensus.Engine) (*DVoteProtocolManager) {
	// Create the protocol manager with the base fields
	manager := &DVoteProtocolManager{
		networkId:   networkId,
		ethManager:  ethManager,
		blockchain:  blockchain,
		lock:        &sync.Mutex{},
		packager:    dpos.NewPackager(config, engine, DefaultConfig.Etherbase, eth, eth.EventMux()),
		dposManager: NewDPoSProtocolManager(eth, ethManager, config, config2, mode, networkId, blockchain, engine),
	}

	currNodeId = discover.PubkeyID(&config2.NodeKey().PublicKey).TerminalString();
	currNodeIdHash = common.Hex2Bytes(currNodeId);
	ElectionInfo0 = nil;
	NextElectionInfo = nil;
	return manager
}

func (pm *DVoteProtocolManager) Start(maxPeers int) {
	// get data from contract
	if DelegatorsTable != nil && len(DelegatorsTable) > 0 {
		log.Info("Starting DPoS Delegation Consensus")
		pm.dposManager.Start();
	} else {
		log.Info("Starting DPoS Voting Consensus")
		pm.packager.Start();
		go pm.schedule();
		if !TestMode {
			time.AfterFunc(time.Second*time.Duration(PackagingInterval), pm.scheduleElecting) //initial attempt.
		}
	}
}

func (pm *DVoteProtocolManager) schedule() {
	t1 := time.NewTimer(time.Second * time.Duration(ElectingInterval))
	t2 := time.NewTimer(time.Second * time.Duration(PackagingInterval))
	for {
		select {
		case <-t1.C:
			go pm.scheduleElecting();
			t1 = time.NewTimer(time.Second * time.Duration(ElectingInterval))
		case <-t2.C:
			go pm.schedulePackaging();
			t2 = time.NewTimer(time.Second * time.Duration(PackagingInterval))
		}
	}
}
func (pm *DVoteProtocolManager) schedulePackaging() {
	// log.Info("schedulePackaging...")
	// generate block by election node.
	if pm.isElectionNode() {
		round := pm.blockchain.CurrentFastBlock().Header().Round;
		block := pm.packager.GenerateNewBlock(round+1, currNodeId);
		block.ToString();
	}
}

func (pm *DVoteProtocolManager) isElectionNode() bool {
	return ElectionInfo0 != nil && ElectionInfo0.electionNodeId == currNodeId;
}

func (pm *DVoteProtocolManager) scheduleElecting() {
	round := uint64(1)
	if NextElectionInfo != nil {
		round = NextElectionInfo.round + 1
	}
	NextElectionInfo = &ElectionInfo{
		round,
		VOTESTATE_LOOKING,
		0, make(map[string]uint32),
		currNodeId,
		currNodeIdHash,
		time.Now().Unix() + int64(ElectingInterval),
		time.Now(),
	};
	log.Info(fmt.Sprintf("Elect for next round %v...", round));
	pm.electNodeSafely();
}

// this is a loop function for electing node.
func (pm *DVoteProtocolManager) electNodeSafely() {
	switch NextElectionInfo.enodestate {
	case VOTESTATE_STOP:
		return;
	case VOTESTATE_LOOKING:
		{
			if TestMode {
				return; // the state machine controlled by the test case in test mode
			}
			// initialize the tickets with the number of all peers connected.
			NextElectionInfo.latestActiveENode = time.Now();
			NextElectionInfo.electionTickets = uint32(len(pm.ethManager.peers.peers));
			if NextElectionInfo.electionTickets == 0 {
				log.Debug("Looking for election node but no any peer found, enode state: " + strconv.Itoa(int(NextElectionInfo.enodestate)));
				// we choose rand number as the interval to reduce the conflict while electing.
				time.AfterFunc(time.Second*time.Duration(rand.Intn(5)), pm.electNodeSafely);
				return;
			}
			log.Debug("Start looking for election node with my tickets: " + strconv.Itoa(int(NextElectionInfo.electionTickets)));
			for _, peer := range pm.ethManager.peers.peers {
				err := peer.SendVoteElectionRequest(&VoteElectionRequest{NextElectionInfo.round,
				NextElectionInfo.electionTickets, currNodeIdHash});
				if (err != nil) {
					log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
				}
			}
			//log.Debug("Start looking for election node... my tickets: " + strconv.Itoa(int(NextElectionInfo.electionTickets)) + ", enodestate: " + strconv.Itoa(int(NextElectionInfo.enodestate)));
			break;
		}
	case VOTESTATE_SELECTED:
		{
			break;
		}
	}
}

func (pm *DVoteProtocolManager) Stop() {
	if DelegatorsTable != nil && len(DelegatorsTable) > 0 {
		pm.dposManager.Stop();
	} else {
		if ElectionInfo0 != nil {
			ElectionInfo0.enodestate = VOTESTATE_STOP;
		}
		if NextElectionInfo != nil {
			NextElectionInfo.enodestate = VOTESTATE_STOP;
		}
		pm.packager.Stop();
	}
	// Quit the sync loop.
	log.Info("DPoS Voting Consensus stopped")
}

// handleMsg is invoked whenever an inbound message is received from a remote
// peer. The remote connection is torn down upon returning any error.
func (pm *DVoteProtocolManager) handleMsg(msg *p2p.Msg, p *peer) error {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	if ElectionInfo0 != nil {
		ElectionInfo0.latestActiveENode = time.Now();
	}
	// Handle the message depending on its contents
	switch {
	case msg.Code == VOTE_ElectionNode_Request:
		var request VoteElectionRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug(fmt.Sprintf("Received request round %v, nodeid %v, CurrRound %v", request.Round, common.Bytes2Hex(request.NodeId), NextElectionInfo.round));
		if request.Round == NextElectionInfo.round {
			if NextElectionInfo.enodestate == VOTESTATE_SELECTED {
				log.Debug("I am in agreed state " + NextElectionInfo.electionNodeId);
				if TestMode {
					return nil;
				}
				return p.SendBroadcastVotedElection(&BroadcastVotedElection{
					NextElectionInfo.round,
					NextElectionInfo.electionTickets,
					VOTESTATE_SELECTED,
					NextElectionInfo.electionNodeIdHash});
			} else {
				if request.Tickets > NextElectionInfo.electionTickets {
					NextElectionInfo.electionNodeIdHash = request.NodeId[:8];
					NextElectionInfo.electionNodeId = common.Bytes2Hex(request.NodeId[:8]);

					log.Debug("Agreed the request node as the election node: " + NextElectionInfo.electionNodeId);
					if TestMode {
						return nil;
					}
					// remote win.
					// broadcast to all peers again.
					for _, peer := range pm.ethManager.peers.peers {
						err := peer.SendBroadcastVotedElection(&BroadcastVotedElection{
							NextElectionInfo.round,
							NextElectionInfo.electionTickets,
							VOTESTATE_SELECTED,
							NextElectionInfo.electionNodeIdHash,
						});
						if (err != nil) {
							log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
						}
					}
				} else {
					log.Debug("I win. simply skip this request.");
					// I win. simply skip this request.
				}
			}
		} else if request.Round < NextElectionInfo.round {
			log.Debug(fmt.Sprintf("Mismatched request.round %v, CurrRound %v: ", request.Round, NextElectionInfo.round))
			if TestMode {
				return nil;
			}
			return p.SendVoteElectionResponse(&VoteElectionResponse{
				NextElectionInfo.round,
				NextElectionInfo.electionTickets,
				NextElectionInfo.activeTime,
				VOTESTATE_MISMATCHED_ROUND,
				NextElectionInfo.electionNodeIdHash});
		} else if request.Round > NextElectionInfo.round {
			if NextElectionInfo.enodestate == VOTESTATE_SELECTED {
				if TestMode {
					return nil;
				}
				return p.SendVoteElectionResponse(&VoteElectionResponse{
					NextElectionInfo.round,
					NextElectionInfo.electionTickets,
					NextElectionInfo.activeTime,
					VOTESTATE_MISMATCHED_ROUND,
					NextElectionInfo.electionNodeIdHash});
			} else if NextElectionInfo.enodestate == VOTESTATE_LOOKING {
				//skip
			}
			log.Debug(fmt.Sprintf("Mismatched request.round %v, CurrRound %v: ", request.Round, NextElectionInfo.round))
			//NextElectionInfo.round

		}
	case msg.Code == VOTE_ElectionNode_Response:
		var response VoteElectionResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Info("Received a voted response: " + common.Bytes2Hex(response.ElectionNodeId));
		if response.State == VOTESTATE_SELECTED {
			log.Debug("Voted Election Response must not have VOTESTATE_SELECTED state. rejected!")
			//VOTE_ElectionNode_Broadcast only have VOTESTATE_SELECTED state.
			return nil;
		} else if response.State == VOTESTATE_MISMATCHED_ROUND && NextElectionInfo.enodestate == VOTESTATE_LOOKING {
			log.Info(fmt.Sprintf("Mismatched round %v, switch to %v", NextElectionInfo.round, response.Round))
			// update round and resend
			NextElectionInfo = &ElectionInfo{
				response.Round,
				VOTESTATE_LOOKING,
				0, make(map[string]uint32),
				currNodeId,
				currNodeIdHash,
				response.ActiveTime,
				time.Now(),
			};
			pm.electNodeSafely()
		}
		return nil;
	case msg.Code == VOTE_ElectionNode_Broadcast:
		var response BroadcastVotedElection;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug("Received broadcast message: " + common.Bytes2Hex(response.ElectionNodeId));
		// just calculate the voted tickets.
		NextElectionInfo.confirmedTickets[common.Bytes2Hex(response.ElectionNodeId)] ++;
		maxTickets, bestNodeId := uint32(0), "";
		for key, value := range NextElectionInfo.confirmedTickets {
			if maxTickets < value {
				maxTickets = value;
				bestNodeId = key;
			}
		}
		if NextElectionInfo.enodestate == VOTESTATE_SELECTED {
			//skip
		} else if NextElectionInfo.enodestate == VOTESTATE_LOOKING && maxTickets >= NextElectionInfo.electionTickets {
			NextElectionInfo.enodestate = VOTESTATE_SELECTED;
			NextElectionInfo.electionNodeId = bestNodeId;
			NextElectionInfo.electionNodeIdHash = []byte(bestNodeId);

			// when do we switch this new data? ElectionInfo0 = NextElectionInfo;
			log.Info("Confirmed the final election node: " + NextElectionInfo.electionNodeId);
			leftTime := NextElectionInfo.activeTime - time.Now().Unix()
			time.AfterFunc(time.Second*time.Duration(leftTime), func() {
				ElectionInfo0 = &ElectionInfo{
					NextElectionInfo.round,
					NextElectionInfo.enodestate,
					NextElectionInfo.electionTickets,
					NextElectionInfo.confirmedTickets,
					NextElectionInfo.electionNodeId,
					NextElectionInfo.electionNodeIdHash,
					NextElectionInfo.activeTime,
					NextElectionInfo.latestActiveENode,
				};
			});
		}
		return nil;
	default:
		return pm.dposManager.handleMsg(msg, p)
	}
	return nil
}