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
)

// DPoS voting handler is purposed on the voting process of all delegators.
// this is the previous process of DPoS delegator packaging process.
// we need to vote at least 31 delegators and 70 candidates in the smart contract.
// if all the conditions satisfied, then activate the delegator packaging process.
// DPoS packaging handler.
var (
	VotingInterval   uint32 = 2;  // vote for packaging node in every 5 seconds.
	ElectingInterval uint32 = 30; // elect for new node in every 30 seconds.

	electionInfo     *ElectionInfo; // we use two versions of election info for switching election node smoothly.
	nextElectionInfo *ElectionInfo;
)

// Delegator table refers to the voting contract.
type DelegatorVotingManager interface {
	Refresh() (DelegatorsTable []string, DelegatorNodes []*discover.Node)
}

type ElectionInfo struct {
	round            uint64;
	enodestate       uint8; //= VOTESTATE_LOOKING
	electionTickets  uint8; //= 0
	electionNodeId   string; //= ""
	electionNodeIdHash []byte; // the election node id.

	latestActiveENode  time.Time;// = time.Now(); // the check whether the election node is active or not.
	// error handlers
	eNodeMismatchCounter uint8; // the mismatching counter of all responses form all election nodes.
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
	electionInfo = nil;
	nextElectionInfo = &ElectionInfo{
		1,
		VOTESTATE_LOOKING,
		0,
		"",
		nil,
		time.Now(),
		0,
	};
	return manager
}

func (pm *DVoteProtocolManager) Start(maxPeers int) {
	log.Info("Starting DPoS Voting Consensus")

	// get data from contract
	if DelegatorsTable != nil && len(DelegatorsTable) > 0 {
		pm.dposManager.Start();
	} else {
		pm.packager.Start();
		go pm.scheduleElecting();
		time.AfterFunc(time.Second*time.Duration(ElectingInterval), pm.scheduleElecting);
		time.AfterFunc(time.Second*time.Duration(VotingInterval), pm.schedulePackaging);
	}
}

func (pm *DVoteProtocolManager) schedulePackaging() {
	pm.packageSafely()
	time.AfterFunc(time.Second*time.Duration(VotingInterval), pm.schedulePackaging);
}

func (pm *DVoteProtocolManager) packageSafely() {
	// generate block by election node.
	if pm.isElectionNode() {
		round := pm.blockchain.CurrentFastBlock().Header().Round;
		block := pm.packager.GenerateNewBlock(round+1, currNodeId);
		block.ToString();
	}
}

func (pm *DVoteProtocolManager) isElectionNode() bool {
	return electionInfo != nil && electionInfo.electionNodeId == currNodeId;
}

func (pm *DVoteProtocolManager) scheduleElecting() {
	log.Info("Elect for next round...");
	round := uint64(1)
	if nextElectionInfo != nil {
		round = nextElectionInfo.round + 1
	}
	nextElectionInfo = &ElectionInfo{
		round,
		VOTESTATE_LOOKING,
		0,
		"",
		nil,
		time.Now(),
		0,
	};
	pm.electNodeSafely();
	time.AfterFunc(time.Second*time.Duration(ElectingInterval), pm.scheduleElecting);
}

// this is a loop function for electing node.
func (pm *DVoteProtocolManager) electNodeSafely() {
	switch nextElectionInfo.enodestate {
	case VOTESTATE_STOP:
		return;
	case VOTESTATE_LOOKING:
		{
			// initialize the tickets with the number of all peers connected.
			nextElectionInfo.latestActiveENode = time.Now();
			nextElectionInfo.eNodeMismatchCounter = 0;
			nextElectionInfo.electionTickets = uint8(len(pm.ethManager.peers.peers));
			if nextElectionInfo.electionTickets == 0 {
				log.Debug("Looking for election node but no any peer found, enode state: " + strconv.Itoa(int(nextElectionInfo.enodestate)));
				// we choose rand number as the interval to reduce the conflict while electing.
				time.AfterFunc(time.Second*time.Duration(rand.Intn(5)), pm.electNodeSafely);
				return;
			}

			for _, peer := range pm.ethManager.peers.peers {
				err := peer.SendVoteElectionRequest(&VoteElectionRequest{nextElectionInfo.round,
				nextElectionInfo.electionTickets, currNodeIdHash});
				if (err != nil) {
					log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
				}
			}
			log.Debug("Start looking for election node... my tickets: " + strconv.Itoa(int(nextElectionInfo.electionTickets)) + ", enodestate: " + strconv.Itoa(int(nextElectionInfo.enodestate)));
			break;
		}
	case VOTESTATE_SELECTED:
		{
			if nextElectionInfo.eNodeMismatchCounter > 1 {
				log.Warn("More then two peers reported different election node, reset election process!");
				nextElectionInfo = &ElectionInfo{
					nextElectionInfo.round + 1,
					VOTESTATE_LOOKING,
					0,
					"",
					nil,
					time.Now(),
					0,
				};
				pm.electNodeSafely();
				return;
			}
			break;
		}
	}
}

func (pm *DVoteProtocolManager) Stop() {
	if DelegatorsTable != nil && len(DelegatorsTable) > 0 {
		pm.dposManager.Stop();
	} else {
		if electionInfo != nil {
			electionInfo.enodestate = VOTESTATE_STOP;
		}
		if nextElectionInfo != nil {
			nextElectionInfo.enodestate = VOTESTATE_STOP;
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
	if electionInfo != nil {
		electionInfo.latestActiveENode = time.Now();
	}
	// Handle the message depending on its contents
	switch {
	case msg.Code == VOTE_ElectionNode_Request:
		var request VoteElectionRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if request.Round == nextElectionInfo.round {
			if nextElectionInfo.enodestate == VOTESTATE_SELECTED {
				log.Debug("Request Node has an election node now " + nextElectionInfo.electionNodeId);
				return p.SendVoteElectionResponse(&VoteElectionResponse{
					nextElectionInfo.round,
					nextElectionInfo.electionTickets,
					VOTESTATE_SELECTED,
					nextElectionInfo.electionNodeIdHash});
			} else {
				if request.Tickets > nextElectionInfo.electionTickets {
					nextElectionInfo.enodestate = VOTESTATE_SELECTED;
					nextElectionInfo.electionNodeIdHash = request.NodeId[:8];
					nextElectionInfo.electionNodeId = common.Bytes2Hex(request.NodeId[:8]);

					log.Info("confirmed the request node as the election node: " + nextElectionInfo.electionNodeId);
					// remote won.
					return p.SendVoteElectionResponse(&VoteElectionResponse{
						nextElectionInfo.round,
						nextElectionInfo.electionTickets,
						VOTESTATE_SELECTED,
						nextElectionInfo.electionNodeIdHash});
				} else {
					// I won.
					nextElectionInfo.enodestate = VOTESTATE_SELECTED;
					nextElectionInfo.electionNodeId = common.Bytes2Hex(currNodeIdHash);
					nextElectionInfo.electionNodeIdHash = currNodeIdHash;

					return p.SendVoteElectionResponse(&VoteElectionResponse{
						nextElectionInfo.round,
						nextElectionInfo.electionTickets,
						VOTESTATE_SELECTED,
						nextElectionInfo.electionNodeIdHash});
				}
			}
		} else if request.Round < nextElectionInfo.round {
			return p.SendVoteElectionResponse(&VoteElectionResponse{
				nextElectionInfo.round,
				nextElectionInfo.electionTickets,
				VOTESTATE_MISMATCHED_ROUND,
				nextElectionInfo.electionNodeIdHash});
		} else if request.Round > nextElectionInfo.round {
			//TODO:
			//nextElectionInfo.round
		}
	case msg.Code == VOTE_ElectionNode_Response:
		var response VoteElectionResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		//log.Info("Received election node response: " + strconv.Itoa(int(response.Tickets)));
		if nextElectionInfo.enodestate == VOTESTATE_SELECTED {
			// error handling.
			if nextElectionInfo.electionNodeId != common.Bytes2Hex(response.ElectionNodeId) {
				nextElectionInfo.eNodeMismatchCounter ++;
			}
			//log.Info("Node has an election node now.");
			return nil;
		} else if nextElectionInfo.enodestate == VOTESTATE_LOOKING && response.State == VOTESTATE_SELECTED {
			nextElectionInfo.enodestate = VOTESTATE_SELECTED;
			nextElectionInfo.electionNodeId = common.Bytes2Hex(response.ElectionNodeId);
			nextElectionInfo.electionNodeIdHash = response.ElectionNodeId;
			electionInfo = nextElectionInfo;
			log.Debug("Confirmed as the election node: " + nextElectionInfo.electionNodeId);
		} else {
			if response.State == VOTESTATE_MISMATCHED_ROUND {
				//todo: update round and resend

			}
		}
		return nil;
	default:
		return pm.dposManager.handleMsg(msg, p)
	}
	return nil
}