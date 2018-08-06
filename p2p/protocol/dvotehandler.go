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
	"fmt"
	"math/rand"
	"bytes"
	"errors"
)

// DPoS voting handler for supporting all degelator voting process.
// this is previous process of DPoS delegator packaging process.
// we need to vote at least 31 delegators and 70 candidate in a contract.
// if all the condidtion satisfied, then activate the delegator packaging process.
// DPoS packaging handler.
var (
	EMPTY_NODEINFO   p2p.NodeInfo;
	VotingInterval   uint32 = 5;  // vote for packaging node in every 5 seconds.
	ElectingInterval uint32 = 600; // elect for new node in every 600 seconds.
	CandidateNumber  uint8 = 21; // we make 21 candidates as the best group for packaging.

	electionInfo     *ElectionInfo; // we use two versions of election info for switching election node smoothly.
	nextElectionInfo *ElectionInfo;
	currCandidatesTable  []string; // only for the election node.
)

type ElectionInfo struct {
	enodestate       uint8; //= STATE_LOOKING
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

	// channels for fetcher, syncer, txsyncLoop
	newPeerCh     chan *peer;
	voteTrigger   chan bool;
	lock          *sync.Mutex; // protects running

	packager      *dpos.Packager;
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
		newPeerCh:   make(chan *peer),
		voteTrigger: make(chan bool),
		lock:        &sync.Mutex{},
		packager:    dpos.NewPackager(config, engine, DefaultConfig.Etherbase, eth, eth.EventMux()),
	}

	currNodeId = discover.PubkeyID(&config2.NodeKey().PublicKey).TerminalString();
	currNodeIdHash = common.Hex2Bytes(currNodeId);
	electionInfo = nil;
	nextElectionInfo = &ElectionInfo{
		STATE_LOOKING,
		0,
		"",
		nil,
		time.Now(),
		0,
	};
	// we make 21 candidates as the best group for packaging.
	currCandidatesTable = make([]string, 0, CandidateNumber);
	return manager
}

func (pm *DVoteProtocolManager) Start(maxPeers int) {
	log.Info("Starting DPoS Voting Consensus")

	pm.packager.Start();
	go pm.electNodeSafely();
	time.AfterFunc(time.Second*time.Duration(ElectingInterval), pm.scheduleElecting);
}

func (pm *DVoteProtocolManager) scheduleVoting() {
	pm.voteTrigger <- true;
	time.AfterFunc(time.Second*time.Duration(VotingInterval), pm.scheduleVoting);
}

func (pm *DVoteProtocolManager) isElectionNode() bool {
	return electionInfo != nil && electionInfo.electionNodeId == currNodeId;
}

func (pm *DVoteProtocolManager) scheduleElecting() {
	log.Info("Elect for next round...");
	nextElectionInfo = &ElectionInfo{
		STATE_LOOKING,
		0,
		"",
		nil,
		time.Now(),
		0,
	};
	pm.electNodeSafely();
	time.AfterFunc(time.Second*time.Duration(ElectingInterval), pm.scheduleElecting);
}

var electingNow = false;
func (pm *DVoteProtocolManager) electNodeSafely() {
	if (electingNow) {
		return;
	}
	electingNow = true;
	pm.electNode0();
	electingNow = false;
}
// this is a loop function for electing node.
func (pm *DVoteProtocolManager) electNode0() {
	switch (nextElectionInfo.enodestate) {
	case STATE_VOTE_STOP:
		return;
	case STATE_LOOKING:
		{
			// initialize the tickets with the number of all peers connected.
			nextElectionInfo.latestActiveENode = time.Now();
			nextElectionInfo.eNodeMismatchCounter = 0;
			nextElectionInfo.electionTickets = uint8(len(pm.ethManager.peers.peers));
			if (nextElectionInfo.electionTickets == 0) {
				log.Debug("Looking for election node but no any peer found, enode state: " + strconv.Itoa(int(nextElectionInfo.enodestate)));
				// we choose rand number as the interval to reduce the conflict while electing.
				time.AfterFunc(time.Second*time.Duration(rand.Intn(5)), pm.electNodeSafely);
				return;
			}

			for _, peer := range pm.ethManager.peers.peers {
				err := peer.SendVoteElectionRequest(&VoteElectionRequest{nextElectionInfo.electionTickets, currNodeIdHash});
				if (err != nil) {
					log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
				}
			}
			log.Debug("Start looking for election node... my tickets: " + strconv.Itoa(int(nextElectionInfo.electionTickets)) + ", enodestate: " + strconv.Itoa(int(nextElectionInfo.enodestate)));
			time.AfterFunc(time.Second*time.Duration(VotingInterval), pm.electNodeSafely);
			break;
		}
	case STATE_VOTE_SELECTED:
		{
			if (nextElectionInfo.eNodeMismatchCounter > 1) {
				log.Warn("More then two peers reported different election node, reset election process!");
				nextElectionInfo = &ElectionInfo{
					STATE_LOOKING,
					0,
					"",
					nil,
					time.Now(),
					0,
				};
				pm.electNodeSafely();
				return;
			}
			//if (len(nextElectionInfo.currCandidatesTable) == nextElectionInfo.electionTickets) {

			//}
			if (nextElectionInfo != electionInfo) {
				electionInfo = nextElectionInfo;
				log.Info("Switched the new election round. node id: " + nextElectionInfo.electionNodeId);
			}
			break;
		}
	}

	if (electionInfo == nil) {
		return;
	}
	switch (electionInfo.enodestate) {
	case STATE_VOTE_STOP:
		return;
	case STATE_VOTE_SELECTED:
		{
			if (!pm.isElectionNode()) {
				pm.registerCandidate();

				//check whether the current election node is still alive or not
				//log.Info("latest check : "+strconv.Itoa(int(time.Now().Unix() - latestActiveENode.Unix())));
				if ((time.Now().Unix() - electionInfo.latestActiveENode.Unix()) >= int64(VotingInterval*3)) {
					log.Info("Election connection is inactive from last " + strconv.Itoa(int(time.Now().Unix()-electionInfo.latestActiveENode.Unix())) + " seconds, reset the election state!");
					electionInfo.latestActiveENode = time.Now();
					currCandidatesTable = make([]string, 0, CandidateNumber);
					nextElectionInfo = &ElectionInfo{
						STATE_LOOKING,
						0,
						"",
						nil,
						time.Now(),
						0,
					};
				}
			}
			//loop for next time.
			time.AfterFunc(time.Second*time.Duration(VotingInterval), pm.electNodeSafely);
			return;
		}
	}
}

// the node would not be a candidate if it is not qualified.
func (pm *DVoteProtocolManager) isCandidateNode() bool {
	for i :=0; i < len(currCandidatesTable); i++ {
		if (currCandidatesTable[i] == currNodeId) {
			return true;
		}
	}
	return false;
}

// register as packaging candidate node.
// we will select the good nodes as the candidates.
func (pm *DVoteProtocolManager) registerCandidate() {
	if (pm.isCandidateNode()) {
		return;
	}
	c := pm.ethManager.peers.PeersById(electionInfo.electionNodeId);
	if (c != nil) {
		// we supported two approches to register the node as candidate.
		// 1. an outside solution for electing candidate such as EOS block produces.
		// 2. a default solution for electing candidate in simple way such as following.
		c.SendRegisterCandidateRequest(&RegisterCandidateRequest{currNodeIdHash});
		log.Debug("Registering myself as candicate...");
	}
}

func (pm *DVoteProtocolManager) Stop() {
	if (electionInfo != nil) {
		electionInfo.enodestate = STATE_VOTE_STOP;
	}
	if (nextElectionInfo != nil) {
		nextElectionInfo.enodestate = STATE_VOTE_STOP;
	}
	pm.packager.Stop();
	// Quit the sync loop.
	log.Info("DPoS Consensus stopped")
}

// handleMsg is invoked whenever an inbound message is received from a remote
// peer. The remote connection is torn down upon returning any error.
func (pm *DVoteProtocolManager) handleMsg(msg *p2p.Msg, p *peer) error {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	if (electionInfo != nil) {
		electionInfo.latestActiveENode = time.Now();
	}
	// Handle the message depending on its contents
	switch {
	case msg.Code == VOTE_ElectionNode_Request:
		var request VoteElectionRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		//log.Info("Received election node request: " + common.Bytes2Hex(request.NodeId) + ", tickets: " + strconv.Itoa(int(request.Tickets)));
		if (nextElectionInfo.enodestate == STATE_VOTE_SELECTED && nextElectionInfo.electionNodeId != "") {
			log.Debug("Request Node has an election node now " + nextElectionInfo.electionNodeId);
			return p.SendVoteElectionResponse(&VoteElectionResponse{nextElectionInfo.electionTickets, STATE_VOTE_SELECTED, nextElectionInfo.electionNodeIdHash});
		} else {
			if (request.Tickets > nextElectionInfo.electionTickets) {
				nextElectionInfo.enodestate = STATE_VOTE_SELECTED;
				nextElectionInfo.electionNodeIdHash = request.NodeId[:8];
				nextElectionInfo.electionNodeId = common.Bytes2Hex(request.NodeId[:8]);

				log.Info("confirmed the request node as the election node: " + nextElectionInfo.electionNodeId);
				// remote won.
				return p.SendVoteElectionResponse(&VoteElectionResponse{nextElectionInfo.electionTickets, STATE_VOTE_SELECTED, nextElectionInfo.electionNodeIdHash});
			} else {
				// I won.
				nextElectionInfo.enodestate = STATE_VOTE_SELECTED;
				nextElectionInfo.electionNodeId = common.Bytes2Hex(currNodeIdHash);
				nextElectionInfo.electionNodeIdHash = currNodeIdHash;

				return p.SendVoteElectionResponse(&VoteElectionResponse{nextElectionInfo.electionTickets, STATE_VOTE_SELECTED, nextElectionInfo.electionNodeIdHash});
			}
		}
	case msg.Code == VOTE_ElectionNode_Response:
		var response VoteElectionResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		//log.Info("Received election node response: " + strconv.Itoa(int(response.Tickets)));
		if (nextElectionInfo.enodestate == STATE_VOTE_SELECTED) {
			// error handling.
			if (nextElectionInfo.electionNodeId != common.Bytes2Hex(response.ElectionNodeId)) {
				nextElectionInfo.eNodeMismatchCounter ++;
			}
			//log.Info("Node has an election node now.");
			return nil;
		}
		if (response.State == STATE_VOTE_SELECTED && response.ElectionNodeId != nil) {
			nextElectionInfo.enodestate = STATE_VOTE_SELECTED;
			nextElectionInfo.electionNodeId = common.Bytes2Hex(response.ElectionNodeId);
			nextElectionInfo.electionNodeIdHash = response.ElectionNodeId;

			log.Debug("Confirmed as the election node: " + nextElectionInfo.electionNodeId);
		}
		return nil;
	case msg.Code == RegisterCandidate_Request:
		if (!pm.isElectionNode()) {
			return nil;
		}
		// Decode the VotePresidentRequest
		var request RegisterCandidateRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if (len(currCandidatesTable) < int(CandidateNumber)) {
			flagb := false;
			flaga := false;
			for i :=0; i < len(currCandidatesTable); i++ {
				if (currCandidatesTable[i] == nextElectionInfo.electionNodeId) {
					flaga = true;
				}
				if (currCandidatesTable[i] == common.Bytes2Hex(request.CandidateId)) {
					flagb = true;
				}
			}
			// we just reuse the previous CandidatesTable for next election pool.
			if (!flaga) {
				currCandidatesTable = append(currCandidatesTable, nextElectionInfo.electionNodeId);
			}
			if (!flagb) {
				currCandidatesTable = append(currCandidatesTable, common.Bytes2Hex(request.CandidateId));
				log.Info("ElectionServer accepted a candidate: " + common.Bytes2Hex(request.CandidateId));
			}
			return p.SendRegisterCandidateResponse(&RegisterCandidateResponse{currCandidatesTable,request.CandidateId, DPOSMSG_SUCCESS });
		} else {
			return p.SendRegisterCandidateResponse(&RegisterCandidateResponse{currCandidatesTable,request.CandidateId, DPOSErroCandidateFull });
		}
	case msg.Code == RegisterCandidate_Response:
		var response RegisterCandidateResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		tableStr := "";
		currCandidatesTable = response.Candidates;
		for i :=0; i<len(currCandidatesTable); i++ {
			tableStr += currCandidatesTable[i] + ",";
		}
		log.Info("Received newest candidate table: " + tableStr);
		if (response.Code == DPOSMSG_SUCCESS) {
			log.Info("Election node confirmed mine as the candidate.");
		} else {
			log.Info("Election node rejected mine as the candidate with error code " + strconv.Itoa(int(response.Code)));
		}
		return nil;
		//----------------------Election process end------------------------------//
	case msg.Code == DPOS_PACKAGE_REQUEST:
		var request PackageRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug("received package request: " + strconv.FormatUint(request.Round, 10));
		if (electionInfo.electionNodeId == p.id && bytes.Equal(electionInfo.electionNodeIdHash, request.ElectionId) && currNodeId == request.PresidentId) {
			if pm.sendPackageResponseToElectionNode(pm.generateBlock(&request)) {
				return nil;
			} else {
				return errors.New("unable to send the response to election node!");
			}
		} else {
			log.Warn("Packaging node Id does not match! Request: {}");
			emptyByte := make([]byte, 0);
			if pm.sendPackageResponseToElectionNode(&PackageResponse{request.Round,
				request.PresidentId, request.ElectionId, common.BytesToHash(emptyByte), DPOSErroPACKAGE_VERIFY_FAILURE }) {
				return nil;
			} else {
				return errors.New("unable to send the response to election node!");
			}
		}
	case msg.Code == DPOS_PACKAGE_RESPONSE:
		var response PackageResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug("received package response: ");
		fmt.Sprintf("%+v", response);
		if (pm.isElectionNode() && response.Code == DPOSMSG_SUCCESS) {
			// got the new generated block and verify.
			//TODO: headerValidator.validateAndLog(response.getBlockHeader(), logger);
			//currVotingPool.confirmSync(currVotingPool.round, response.PresidentId);
			return nil;
		} else {
			log.Warn("Packaging response error! Elected president node performs bad, remove it from candicate list. Response: {}", response);
			if (!bytes.Equal(electionInfo.electionNodeIdHash, response.ElectionId)) {
				return errors.New("Packaging election Id does not match! Elected president node performs bad, remove it from candicate list. Response:");
			}
			if (response.Code == DPOSErroPACKAGE_EMPTY) {
				// it's empty package, reset voting pool. reset.
				return errors.New("Packaging block is skipped due to there was no transaction found at the remote peer.");
			}
			if (response.Code == DPOSErroPACKAGE_NOTSYNC) {
				return errors.New("Blocks syncing of Elected president has not completed yet. remove it from candicate list. Response: {}");
			}
		}
	default:
		return errResp(ErrInvalidMsgCode, "%v", msg.Code)
	}
	return nil
}

func (pm *DVoteProtocolManager) generateBlock(request *PackageRequest) *PackageResponse {
	block := pm.packager.GenerateNewBlock(request.Round, request.PresidentId);
	block.ToString();
	return &PackageResponse{
		request.Round,
	request.PresidentId,
	request.ElectionId,
	block.Hash(),
	DPOSMSG_SUCCESS};
}

func (self *DVoteProtocolManager) sendPackageResponseToElectionNode(response *PackageResponse) bool {
	c := self.ethManager.peers.PeersById(electionInfo.electionNodeId);
	if (c == nil) {
		log.Warn("Election peer does not exit! unable to send response to the election node.");
		return false;
	}
	c.SendPackageResponse(response);
	return true;
}

func (self *DVoteProtocolManager) sendConfirmedSyncToElectionNode(response *ConfirmedSyncMessage) bool {
	c := self.ethManager.peers.PeersById(electionInfo.electionNodeId);
	if (c == nil) {
		log.Warn("Election peer does not exit! unable to send response to the election node.");
		return false;
	}
	c.SendConfirmedSyncMessage(response);
	return true;
}

func (self *DVoteProtocolManager) sendPackageRequest(response *PackageRequest) bool {
	c := self.ethManager.peers.PeersById(response.PresidentId);
	if (c == nil) {
		log.Warn("Peer does not exit! unable to send packaging request to voted node " + response.PresidentId);
		return false;
	}
	c.SendPackageRequest(response);
	return true;
}
