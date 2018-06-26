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
	"encoding/json"
	"time"
	"strings"
	"math/rand"
	"strconv"
	"bytes"
	"sync"

	"github.com/juchain/go-juchain/core"
	"github.com/juchain/go-juchain/p2p/protocol/downloader"
	"github.com/juchain/go-juchain/common/log"
	"github.com/juchain/go-juchain/p2p"
	"github.com/juchain/go-juchain/p2p/discover"
	"github.com/juchain/go-juchain/p2p/node"
	"github.com/juchain/go-juchain/common"
	"github.com/pkg/errors"

)

// DPoS packaging handler.

var (
	EMPTY_NODEINFO   p2p.NodeInfo;
	VotingInterval   uint32 = 3;  // vote for packaging block in every 3 seconds.
	ElectingInterval uint32 = 60; // elect for new node in every 60 seconds.
	CandidateNumber  uint8 = 21; // we make 21 candidates as the best group for packaging.

	electionInfo     *ElectionInfo; // we use two versions of election info for switching election node smoothly.
	nextElectionInfo *ElectionInfo;
	currNodeId       string; //= "" current short node id.
	currNodeIdHash   []byte; // short node id hash.
	currVotingPool   *LocalVoteInfo; // only for the election node.
	currCandidatesTable  []string; // only for the election node.

	blockchainRef *core.BlockChain;
	PM *DPoSProtocolManager;
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

type DPoSProtocolManager struct {
	networkId     uint64;
	ethManager    *ProtocolManager;
	blockchain    *core.BlockChain;
	SubProtocols  []p2p.Protocol;
	// channels for fetcher, syncer, txsyncLoop
	newPeerCh     chan *peer;
	lock          *sync.Mutex; // protects running
}

// NewProtocolManager returns a new ethereum sub protocol manager. The Ethereum sub protocol manages peers capable
// with the ethereum network.
func NewDPoSProtocolManager(ethManager *ProtocolManager, config *node.Config, mode downloader.SyncMode, networkId uint64, blockchain *core.BlockChain) (*DPoSProtocolManager) {
	// Create the protocol manager with the base fields
	blockchainRef = blockchain;
	manager := &DPoSProtocolManager{
		networkId:   networkId,
		blockchain:  blockchain,
		ethManager:  ethManager,
		newPeerCh:   make(chan *peer),
		lock:        &sync.Mutex{},
	}

	// Initiate a sub-protocol for every implemented version we can handle
	PM = manager;
	currNodeId = discover.PubkeyID(&config.NodeKey().PublicKey).TerminalString();
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

func (pm *DPoSProtocolManager) Start(maxPeers int) {
	log.Info("Starting DPoS Consensus")

	go pm.electNodeSafely();
	time.AfterFunc(time.Second*time.Duration(ElectingInterval), pm.electNextNode);
}

func (pm *DPoSProtocolManager) isElectionNode() bool {
	return electionInfo != nil && electionInfo.electionNodeId == currNodeId;
}

func (pm *DPoSProtocolManager) electNextNode() {
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
	time.AfterFunc(time.Second*time.Duration(ElectingInterval), pm.electNextNode);
}

var electingNow = false;
func (pm *DPoSProtocolManager) electNodeSafely() {
	if (electingNow) {
		return;
	}
	electingNow = true;
	pm.electNode0();
	electingNow = false;
}
// this is a loop function for electing node.
func (pm *DPoSProtocolManager) electNode0() {
	switch (nextElectionInfo.enodestate) {
	case STATE_STOP:
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
	case STATE_SELECTED:
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
	case STATE_STOP:
		return;
	case STATE_SELECTED:
		{
			if (!pm.isElectionNode()) {
				pm.registerCandidate();

				//check whether the current election node is still alive or not
				//log.Info("latest check : "+strconv.Itoa(int(time.Now().Unix() - latestActiveENode.Unix())));
				if ((time.Now().Unix() - electionInfo.latestActiveENode.Unix()) >= int64(VotingInterval*3)) {
					log.Info("Election connection is inactive from last " + strconv.Itoa(int(time.Now().Unix()-electionInfo.latestActiveENode.Unix())) + " seconds, reset the election state!");
					electionInfo = nil;
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
func (pm *DPoSProtocolManager) isCandidateNode() bool {
	for i :=0; i < len(currCandidatesTable); i++ {
		if (currCandidatesTable[i] == currNodeId) {
			return true;
		}
	}
	return false;
}

// register as packaging candidate node.
// we will select the good nodes as the candidates.
func (pm *DPoSProtocolManager) registerCandidate() {
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

func (pm *DPoSProtocolManager) Stop() {
	log.Info("Stopping DPoS Consensus")
	if (electionInfo != nil) {
		electionInfo.enodestate = STATE_STOP;
	}
	if (nextElectionInfo != nil) {
		nextElectionInfo.enodestate = STATE_STOP;
	}
	// Quit the sync loop.

	log.Info("DPoS Consensus stopped")
}

func (pm *DPoSProtocolManager) newPeer(pv int, p *p2p.Peer, rw p2p.MsgReadWriter) *peer {
	return newPeer(pv, p, newMeteredMsgWriter(rw))
}

// handleMsg is invoked whenever an inbound message is received from a remote
// peer. The remote connection is torn down upon returning any error.
func (pm *DPoSProtocolManager) handleMsg(msg *p2p.Msg, p *peer) error {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	// Handle the message depending on its contents
	switch {
	case msg.Code == VOTE_ElectionNode_Request:
		var request VoteElectionRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		//log.Info("Received election node request: " + common.Bytes2Hex(request.NodeId) + ", tickets: " + strconv.Itoa(int(request.Tickets)));
		if (nextElectionInfo.enodestate == STATE_SELECTED && nextElectionInfo.electionNodeId != "") {
			log.Debug("Request Node has an election node now " + nextElectionInfo.electionNodeId);
			return p.SendVoteElectionResponse(&VoteElectionResponse{nextElectionInfo.electionTickets, STATE_SELECTED, nextElectionInfo.electionNodeIdHash});
		} else {
			if (request.Tickets > nextElectionInfo.electionTickets) {
				nextElectionInfo.enodestate = STATE_SELECTED;
				nextElectionInfo.electionNodeIdHash = request.NodeId[:8];
				nextElectionInfo.electionNodeId = common.Bytes2Hex(request.NodeId[:8]);

				log.Info("confirmed the request node as the election node: " + nextElectionInfo.electionNodeId);
				// remote won.
				return p.SendVoteElectionResponse(&VoteElectionResponse{nextElectionInfo.electionTickets, STATE_SELECTED, nextElectionInfo.electionNodeIdHash});
			} else {
				// I won.
				nextElectionInfo.enodestate = STATE_SELECTED;
				nextElectionInfo.electionNodeId = common.Bytes2Hex(currNodeIdHash);
				nextElectionInfo.electionNodeIdHash = currNodeIdHash;

				return p.SendVoteElectionResponse(&VoteElectionResponse{nextElectionInfo.electionTickets, STATE_SELECTED, nextElectionInfo.electionNodeIdHash});
			}
		}
	case msg.Code == VOTE_ElectionNode_Response:
		var response VoteElectionResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		//log.Info("Received election node response: " + strconv.Itoa(int(response.Tickets)));
		if (nextElectionInfo.enodestate == STATE_SELECTED) {
			// error handling.
			if (nextElectionInfo.electionNodeId != common.Bytes2Hex(response.ElectionNodeId)) {
				nextElectionInfo.eNodeMismatchCounter ++;
			}
			//log.Info("Node has an election node now.");
			return nil;
		}
		if (response.State == STATE_SELECTED && response.ElectionNodeId != nil) {
			nextElectionInfo.enodestate = STATE_SELECTED;
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
			log.Info("Election node confirmed as candidate.");
		} else {
			log.Info("Election node rejected as candidate with error code " + strconv.Itoa(int(response.Code)));
		}
		return nil;
//----------------------Election process end------------------------------//


//----------------------Vote process start--------------------------------//
	case msg.Code == VOTE_PRESIDENT_Request:
		electionInfo.latestActiveENode = time.Now();
		var request VotePresidentRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug("received a vote request: ");
		if (request.CandicateIds == nil || len(request.CandicateIds) == 0) {
			log.Warn("VotePresidentRequest is invalid: {}");
			if (pm.sendVoteResponseToElectionNode(&VotePresidentResponse{request.Round, byte(0),
				request.ElectionId, DPOSErroVOTE_VERIFY_FAILURE})) {
				return nil;
			} else {
				return errors.New("unable to send the response to election node!");
			}
		}
		length := len(request.CandicateIds);
		selectedPId := rand.Intn(length); //TODO: use this strategy by default.
		log.Debug("voted for president id: " + strconv.Itoa(selectedPId));
		if pm.sendVoteResponseToElectionNode(&VotePresidentResponse{request.Round, uint8(selectedPId),
			request.ElectionId, DPOSMSG_SUCCESS}) {
			return nil;
		} else {
			return errors.New("unable to send the response to election node!");
		}
	case msg.Code == VOTE_PRESIDENT_Response:
		var response VotePresidentResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug("received a vote response: ");
		if (bytes.Equal(electionInfo.electionNodeIdHash, response.ElectionId) && response.Code == DPOSMSG_SUCCESS && currVotingPool.round == response.Round) {
			currVotingPool.voteFor(response.CandicateIndex);
			return nil;
		} else {
			log.Warn("VotePresidentResponse error with result: {}");
			if currVotingPool.round != response.Round {
				errors.New("VotePresidentResponse round Id does not match current round! result: ");
			}
			if (!bytes.Equal(electionInfo.electionNodeIdHash, response.ElectionId)) {
				return errors.New("Packaging election Id does not match! Elected president node performs bad, remove it from candicate list. Response:");
			}
			//TODO:
			return nil;
		}
	case msg.Code == DPOS_PACKAGE_REQUEST:
		var request PackageRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		log.Debug("received package request: ");
		if (electionInfo.electionNodeId == p.id && bytes.Equal(electionInfo.electionNodeIdHash, request.ElectionId) && currNodeId == request.PresidentId) {
			// check the best block whether is synchronized or not.
			//SyncStatus syncStatus = syncManager.getSyncStatus();
			//if (1000 > (pm.blockchain.CurrentBlock().NumberU64() + 1)) {//allowed 1 block gap.
			//	log.Info("Failed to package block due to blocks syncing is not completed yet. {}", syncStatus.toString());
			//	pm.sendPackageResponseToElectionNode(&PackageResponse{request.round, request.presidentId,
			//		request.electionId, nil,DPOSErroPACKAGE_NOTSYNC});
			//	return nil;
			//}

			if pm.sendPackageResponseToElectionNode(pm.generateBlock()) {
				return nil;
			} else {
				return errors.New("unable to send the response to election node!");
			}
		} else {
			log.Warn("Packaging node Id does not match! Request: {}");
			if pm.sendPackageResponseToElectionNode(&PackageResponse{request.Round,
				request.PresidentId, request.ElectionId, nil, DPOSErroPACKAGE_VERIFY_FAILURE }) {
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
		if (p.id == currVotingPool.selectNodeId && response.Code == DPOSMSG_SUCCESS) {
			// got the new generated block and verify.
			//TODO: headerValidator.validateAndLog(response.getBlockHeader(), logger);
			currVotingPool.confirmSync(currVotingPool.round, response.PresidentId);
			return nil;
		} else {
			log.Warn("Packaging response error! Elected president node performs bad, remove it from candicate list. Response: {}", response);
			if (!bytes.Equal(electionInfo.electionNodeIdHash, response.ElectionId) || p.id != currVotingPool.selectNodeId) {
				return errors.New("Packaging election Id does not match! Elected president node performs bad, remove it from candicate list. Response:");
			}
			if (response.Code == DPOSErroPACKAGE_EMPTY) {
				// it's empty package, reset voting pool. reset.
				currVotingPool = nil;
				return errors.New("Packaging block is skipped due to there was no transaction found at the remote peer.");
			}
			if (response.Code == DPOSErroPACKAGE_NOTSYNC) {
				currVotingPool.confirmSyncFailed(response.PresidentId);
				return errors.New("Blocks syncing of Elected president has not completed yet. remove it from candicate list. Response: {}");
			}
		}
	default:
		return errResp(ErrInvalidMsgCode, "%v", msg.Code)
	}
	return nil
}

func (pm *DPoSProtocolManager) generateBlock() *PackageResponse {
	//TODO:
	return &PackageResponse{};
}

func (self *DPoSProtocolManager) sendVoteRequest(request *VotePresidentRequest) bool {
	flag := false;
	for _, peer := range self.ethManager.peers.peers {
		peer.SendVotePresidentRequest(request);
		flag = true;
	}
	return flag;
}

func (self *DPoSProtocolManager) sendPackageResponseToElectionNode(response *PackageResponse) bool {
	c := self.ethManager.peers.PeersById(electionInfo.electionNodeId);
	if (c == nil) {
		log.Warn("Election p2p channel does not exit! unable to send response to the election node.");
		return false;
	}
	c.SendPackageResponse(response);
	return true;
}

func (self *DPoSProtocolManager) sendVoteResponseToElectionNode(response *VotePresidentResponse) bool {
	c := self.ethManager.peers.PeersById(electionInfo.electionNodeId);
	if (c == nil) {
		log.Warn("Election p2p channel does not exit! unable to send response to the election node.");
		return false;
	}
	c.SendVotePresidentResponse(response);
	return true;
}

func (self *DPoSProtocolManager) sendConfirmedSyncToElectionNode(response *ConfirmedSyncMessage) bool {
	c := self.ethManager.peers.PeersById(electionInfo.electionNodeId);
	if (c == nil) {
		log.Warn("Election p2p channel does not exit! unable to send response to the election node.");
		return false;
	}
	c.SendConfirmedSyncMessage(response);
	return true;
}

func (self *DPoSProtocolManager) sendPackageRequest(response *PackageRequest) bool {
	c := self.ethManager.peers.PeersById(response.PresidentId);
	if (c == nil) {
		log.Warn("Channel does not exit! unable to send packaging request to voted node " + response.PresidentId);
		return false;
	}
	c.SendPackageRequest(response);
	return true;
}



// start voting for president and packaging block in every round.

func scheduleVoting() {
	if (currNodeId == electionInfo.electionNodeId && len(currCandidatesTable) > 0) {
		time.AfterFunc(time.Duration(VotingInterval*1000), voteForNewPresident);
	}
}

func voteForNewPresident() {
	round := uint64(1);
	// copy all candidates' table.
	currCandidates := make([]string, len(currCandidatesTable));
	copy(currCandidates, currCandidatesTable);
	if (currVotingPool != nil) {
		// to make sure the health candidate pool of next voting, we'd better to remove the unconfirmed node
		// due to any possible issue including blocks in syncing, network unstability and etc.
		round = currVotingPool.round + 1;
		unconfirmedNode := make([]string, 0, len(currVotingPool.confirmedPool));
		for k, v := range currVotingPool.confirmedPool {
			if v == 0 {
				unconfirmedNode = append(unconfirmedNode, k);
			}
		}
		if (len(unconfirmedNode) > 0) {
			log.Info("Unconfirmed sync block candidates" + strings.Join(unconfirmedNode, ", "));
		}
		// let's remove the unconfirmed candidates for next round.
		for _, nodeId := range unconfirmedNode {
			for i, n := range currCandidates {
				if (nodeId == n) {
					removeCanditate(currCandidates, i);
					break;
				}
			}
		}
	} else {
		// query the round number from last block.
		round = blockchainRef.CurrentFastBlock().Header().Round + 1;
	}
	if (len(currCandidates) == 0) {
		// no any qualified candidate. set for next round.
		currVotingPool = nil;
		log.Warn("no any candidate confirmed the block synced in this round "+strconv.FormatUint(round, 10)+", revote again!");
		return ;
	}
	if (round < 1) {
		round = 1;
	}

	currVotingPool = NewLocalVoteInfo(currCandidates, round);
	vrequest := &VotePresidentRequest{round, currVotingPool.getCandicatesIndex(),
		electionInfo.electionNodeIdHash};
	// broadcast this vote request to all nodes.
	if (PM.sendVoteRequest(vrequest)) {
		log.Debug("Voting the presidents: " + currVotingPool.toString());

		// wait for voting result in the gap of interval/2 ms which is applicable.
		time.Sleep(time.Duration(VotingInterval * 1000 / 2));

		// new request for packaging the block to remote peer.
		selectNode, p, err := currVotingPool.maxTicket();
		if (err != nil) {
			log.Warn("no any candidate confirmed the block synced in this round "+strconv.FormatUint(round, 10)+", revote again!");
			return;
		}
		prequest := &PackageRequest{currVotingPool.round, selectNode,
			electionInfo.electionNodeIdHash};
		if (PM.sendPackageRequest(prequest)) {
			// all candidates must sending the confirmed sync message after mining new block.
			// this is important to make sure who are the best candidates in the next round.
			log.Info("Voted info: "+currVotingPool.toString()+", Node info: " + selectNode);
		} else {
			removeCanditate(currCandidatesTable, int(p));
			currVotingPool = nil;
			log.Warn("Failed to package the block from president("+prequest.PresidentId+") with "+strconv.FormatUint(prequest.Round, 10)+" round, revote again!");
		}
	} else {
		log.Debug("Discard this round during none of candidate's connection existing: " + currVotingPool.toString());
		currVotingPool = nil;
	}

	time.AfterFunc(time.Duration(VotingInterval* 1000), voteForNewPresident);
}
func removeCanditate(s []string, i int) []string {
	s[len(s)-1], s[i] = s[i], s[len(s)-1]
	return s[:len(s)-1]
}
func NewLocalVoteInfo(currCandidatesTable []string, round uint64)(*LocalVoteInfo) {
	if (len(currCandidatesTable) >= 8) {
		log.Warn("candicates of each round must be less then from 8");
	}
	confirmedPool := make(map[string]uint32);
	votingPool := make(map[uint8]uint32);
	votingCandidcates := make(map[uint32]string);
	for i, c := range currCandidatesTable {
		votingCandidcates[uint32(i)] = c;
		votingPool[uint8(i)] = 0;
		confirmedPool[c] = 0;
	}
	return &LocalVoteInfo{false, round, "",confirmedPool,
		votingPool, votingCandidcates};
}
type LocalVoteInfo struct {
	isClosed           bool
	round              uint64
	selectNodeId       string; // current id of selected node for packaging.
	confirmedPool      map[string]uint32
	votingPool         map[uint8]uint32
	votingCandidcates  map[uint32]string
}
func (t *LocalVoteInfo) voteFor(candicateIndex uint8) {
	if (t.isClosed) {
		return;
	}
	v, ok := t.votingPool[candicateIndex];
	if (ok) {
		t.votingPool[candicateIndex] = v+1;
		log.Debug("Just voted for president id: " + strconv.Itoa(int(candicateIndex)) + ", total tickets: "+ strconv.Itoa(int(v)) +" in "+ strconv.FormatUint(t.round, 10) +" round.");
	}
}
func (t *LocalVoteInfo) confirmSync(round uint64, nodeId string) {
	if (t.isClosed) {
		return;
	}
	if (round == t.round) {
		v, ok:=t.confirmedPool[nodeId];
		if (ok) {
			t.confirmedPool[nodeId] = v+1;
			log.Debug("Just confirmed syncing completed "+nodeId+" in "+strconv.FormatUint(t.round, 10)+" round.");
		} else {
			// allows to be joined again.
			t.confirmedPool[nodeId] = 1;
		}
	}
}
func (t *LocalVoteInfo) confirmSyncFailed(pnodeId string) {
	if (t.isClosed) {
		return;
	}
	for _, v := range t.votingCandidcates {
		if (v != pnodeId) {
			t.confirmedPool[pnodeId] = 1;
		}
	}
}
func (t *LocalVoteInfo) getCandicatesIndex() ([]uint8) {
	indexes := make([]uint8, len(t.votingPool))
	i := 0;
	for k, _ := range t.votingPool {
		indexes[i] = k;
		i++;
	}
	return indexes;
}
func (t *LocalVoteInfo) maxTicket() (string, uint8, error) {
	t.isClosed = true;
	if (len(t.votingPool) == 0) {
		return "", 0, errors.New("No voting information for any candidate!");
	}
	m := uint32(0);
	position := uint8(0);
	for i, v := range t.votingPool {
		if (v > m) {
			m = v;
		}
		position = i;
	}
	t.selectNodeId = t.votingCandidcates[m];
	return t.votingCandidcates[m], position, nil;
}
func (t *LocalVoteInfo) minTicket() (string, uint8, error) {
	t.isClosed = true;
	if (len(t.votingPool) == 0) {
		return "", 0, errors.New("No voting information for any candidate!");
	}
	m := uint32(0xFFFFFFFF);
	position := uint8(0);
	for i, v := range t.votingPool {
		if (v < m) {
			m = v;
		}
		position = i;
	}
	return t.votingCandidcates[m], position, nil;
}
func (t *LocalVoteInfo) toString() string {
	jsonString, _ := json.Marshal(t.votingPool);
	jsonString2, _ := json.Marshal(t.confirmedPool);
	return "{round: " + strconv.FormatUint(t.round, 10) + ", votingPool: " + string(jsonString) + ", confirmedPool: " +string(jsonString2)+ " }";
}