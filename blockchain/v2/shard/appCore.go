package shard

import (
	"errors"
	"fmt"
	"github.com/incognitochain/incognito-chain/blockchain"
	v2 "github.com/incognitochain/incognito-chain/blockchain/v2"
	"github.com/incognitochain/incognito-chain/common"
	"github.com/incognitochain/incognito-chain/common/base58"
	"github.com/incognitochain/incognito-chain/incognitokey"
	"github.com/incognitochain/incognito-chain/metadata"
	"github.com/incognitochain/incognito-chain/privacy"
	"github.com/incognitochain/incognito-chain/transaction"
	"sort"
	"strconv"
	"strings"
)

type CoreApp struct {
	AppData
}

//==============================Create Block Logic===========================
func (s *CoreApp) preCreateBlock(state *CreateNewBlockState) error {
	bc := state.bc
	beaconHeight, err := state.bc.GetCurrentBeaconHeight()
	if err != nil {
		return err
	}
	curView := state.curView

	// limit number of beacon block confirmed in a shard block
	if beaconHeight-curView.GetHeight() > blockchain.MAX_BEACON_BLOCK {
		beaconHeight = curView.GetHeight() + blockchain.MAX_BEACON_BLOCK
	}

	// we only confirm shard blocks within an epoch
	epoch := beaconHeight / bc.GetChainParams().Epoch

	if epoch > curView.GetEpoch() { // if current epoch > curView 1 epoch, we only confirm beacon block within an epoch
		beaconHeight = curView.GetEpoch() * bc.GetChainParams().Epoch
		epoch = curView.GetEpoch() + 1
	}

	state.newBlockEpoch = epoch
	state.newConfirmBeaconHeight = beaconHeight
	toShard := state.curView.ShardID
	state.crossShardBlocks = state.bc.GetAllValidCrossShardBlockFromPool(toShard)
	return nil
}

func (s *CoreApp) buildTxFromCrossShard(state *CreateNewBlockState) error {
	toShard := state.curView.ShardID
	crossTransactions := make(map[byte][]blockchain.CrossTransaction)
	// Get Cross Shard Block
	for fromShard, crossShardBlock := range state.crossShardBlocks {
		sort.SliceStable(crossShardBlock[:], func(i, j int) bool {
			return crossShardBlock[i].Header.Height < crossShardBlock[j].Header.Height
		})
		indexs := []int{}
		startHeight := state.bc.GetLatestCrossShard(fromShard, toShard)
		for index, crossShardBlock := range crossShardBlock {
			if crossShardBlock.Header.Height <= startHeight {
				break
			}
			nextHeight := state.bc.GetNextCrossShard(fromShard, toShard, startHeight)

			if nextHeight > crossShardBlock.Header.Height {
				continue
			}

			if nextHeight < crossShardBlock.Header.Height {
				break
			}

			startHeight = nextHeight

			err := state.bc.ValidateCrossShardBlock(crossShardBlock)
			if err != nil {
				break
			}
			indexs = append(indexs, index)
		}

		for _, index := range indexs {
			blk := crossShardBlock[index]
			crossTransaction := blockchain.CrossTransaction{
				OutputCoin:       blk.CrossOutputCoin,
				TokenPrivacyData: blk.CrossTxTokenPrivacyData,
				BlockHash:        *blk.Hash(),
				BlockHeight:      blk.Header.Height,
			}
			crossTransactions[blk.Header.ShardID] = append(crossTransactions[blk.Header.ShardID], crossTransaction)
		}
	}

	for _, crossTransaction := range crossTransactions {
		sort.SliceStable(crossTransaction[:], func(i, j int) bool {
			return crossTransaction[i].BlockHeight < crossTransaction[j].BlockHeight
		})
	}
	s.CreateBlock.crossShardTx = crossTransactions
	return nil
}

func (s *CoreApp) buildTxFromMemPool(state *CreateNewBlockState) error {
	txsToAdd, txToRemove, _ := state.bc.GetPendingTransaction(state.curView.ShardID)
	if len(txsToAdd) == 0 {
		s.Logger.Info("Creating empty block...")
	}
	s.CreateBlock.txToRemove = txToRemove
	s.CreateBlock.txsToAdd = txsToAdd
	return nil
}

func (s *CoreApp) buildResponseTxFromTxWithMetadata(state *CreateNewBlockState) error {
	blkProducerPrivateKey := createTempKeyset()
	txRequestTable := map[string]metadata.Transaction{}
	txsRes := []metadata.Transaction{}
	//process withdraw reward
	for _, tx := range state.txsToAdd {
		if tx.GetMetadataType() == metadata.WithDrawRewardRequestMeta {
			requestMeta := tx.GetMetadata().(*metadata.WithDrawRewardRequest)
			requester := base58.Base58Check{}.Encode(requestMeta.PaymentAddress.Pk, common.Base58Version)
			txRequestTable[requester] = tx
		}
	}
	for _, value := range txRequestTable {
		txRes, err := s.buildWithDrawTransactionResponse(&value, &blkProducerPrivateKey)
		if err != nil {
			return err
		} else {
			s.Logger.Infof("[Reward] - BuildWithDrawTransactionResponse for tx %+v, ok: %+v\n", value, txRes)
		}
		txsRes = append(txsRes, txRes)
	}
	s.CreateBlock.txsFromMetadataTx = txsRes
	return nil
}

func (s *CoreApp) processBeaconInstruction(state *CreateNewBlockState) error {
	shardID := state.curView.ShardID
	producerPrivateKey := createTempKeyset()
	responsedTxs := []metadata.Transaction{}
	shardPendingValidator, err := incognitokey.CommitteeKeyListToString(state.bc.GetShardPendingCommittee(state.curView.ShardID))
	if err != nil {
		panic(err)
	}
	assignInstructions := [][]string{}
	stakingTx := make(map[string]string)
	for _, beaconBlock := range state.beaconBlocks {
		for _, l := range beaconBlock.GetInstructions() {

			if l[0] == blockchain.SwapAction {
				autoStaking, err := state.bc.FetchAutoStakingByHeight(beaconBlock.GetHeight())
				if err != nil {
					break
				}
				for _, outPublicKeys := range strings.Split(l[2], ",") {
					// If out public key has auto staking then ignore this public key
					if _, ok := autoStaking[outPublicKeys]; ok {
						continue
					}
					tx, err := s.buildReturnStakingAmountTx(outPublicKeys, &producerPrivateKey)
					if err != nil {
						s.Logger.Error(err)
						continue
					}
					responsedTxs = append(responsedTxs, tx)
				}
			}

			// Process Assign Instruction
			if l[0] == blockchain.AssignAction && l[2] == "shard" {
				if strings.Compare(l[3], strconv.Itoa(int(shardID))) == 0 {
					shardPendingValidator = append(shardPendingValidator, strings.Split(l[1], ",")...)
					assignInstructions = append(assignInstructions, l)
				}
			}
			// Get Staking Tx
			// assume that stake instruction already been validated by beacon committee
			if l[0] == blockchain.StakeAction && l[2] == "beacon" {
				beacon := strings.Split(l[1], ",")
				newBeaconCandidates := []string{}
				newBeaconCandidates = append(newBeaconCandidates, beacon...)
				if len(l) == 6 {
					for i, v := range strings.Split(l[3], ",") {
						txHash, err := common.Hash{}.NewHashFromStr(v)
						if err != nil {
							continue
						}
						txShardID, _, _, _, err := state.bc.GetTransactionByHash(*txHash)
						if err != nil {
							continue
						}
						if txShardID != shardID {
							continue
						}
						// if transaction belong to this shard then add to shard beststate
						stakingTx[newBeaconCandidates[i]] = v
					}
				}
			}
			if l[0] == blockchain.StakeAction && l[2] == "shard" {
				shard := strings.Split(l[1], ",")
				newShardCandidates := []string{}
				newShardCandidates = append(newShardCandidates, shard...)
				if len(l) == 6 {
					for i, v := range strings.Split(l[3], ",") {
						txHash, err := common.Hash{}.NewHashFromStr(v)
						if err != nil {
							continue
						}
						txShardID, _, _, _, err := state.bc.GetTransactionByHash(*txHash)
						if err != nil {
							continue
						}
						if txShardID != shardID {
							continue
						}
						// if transaction belong to this shard then add to shard beststate
						stakingTx[newShardCandidates[i]] = v
					}
				}
			}
		}
	}
	s.CreateBlock.newShardPendingValidator = shardPendingValidator
	s.CreateBlock.stakingTx = stakingTx
	s.CreateBlock.txsFromBeaconInstruction = responsedTxs
	return nil
}

func (s *CoreApp) generateInstruction(state *CreateNewBlockState) error {
	beaconHeight := state.newConfirmBeaconHeight
	shardCommittee, _ := incognitokey.CommitteeKeyListToString(state.curView.ShardCommittee)
	shardID := state.curView.ShardID
	var (
		instructions          = [][]string{}
		swapInstruction       = []string{}
		shardPendingValidator = state.newShardPendingValidator
		err                   error
	)
	if beaconHeight%state.bc.GetChainParams().Epoch == 0 && state.curView.Block.Header.BeaconHeight == beaconHeight {
		maxShardCommitteeSize := state.bc.GetChainParams().MaxShardCommitteeSize
		minShardCommitteeSize := state.bc.GetChainParams().MinShardCommitteeSize

		s.Logger.Info("ShardPendingValidator", state.newShardPendingValidator)
		s.Logger.Info("ShardCommittee", shardCommittee)
		s.Logger.Info("MaxShardCommitteeSize", maxShardCommitteeSize)
		s.Logger.Info("ShardID", shardID)

		swapInstruction, shardPendingValidator, shardCommittee, err = blockchain.CreateSwapAction(shardPendingValidator, shardCommittee, maxShardCommitteeSize, minShardCommitteeSize, shardID, map[string]uint8{}, map[string]uint8{}, state.bc.GetChainParams().Offset, state.bc.GetChainParams().SwapOffset)
		if err != nil {
			s.Logger.Error(err)
			return err
		}
	}
	if len(swapInstruction) > 0 {
		instructions = append(instructions, swapInstruction)
	}
	s.CreateBlock.instruction = instructions
	return nil
}

func (CoreApp) buildWithDrawTransactionResponse(transaction *metadata.Transaction, pkey *privacy.PrivateKey) (metadata.Transaction, error) {
	return nil, nil
}

func (CoreApp) buildReturnStakingAmountTx(s string, pkey *privacy.PrivateKey) (metadata.Transaction, error) {
	return nil, nil
}

func (s *CoreApp) buildHeader(state *CreateNewBlockState) error {
	shardID := state.curView.ShardID

	totalTxsFee := make(map[common.Hash]uint64)
	for _, tx := range state.newBlock.Body.Transactions {
		totalTxsFee[*tx.GetTokenID()] += tx.GetTxFee()
		txType := tx.GetType()
		if txType == common.TxCustomTokenPrivacyType {
			txCustomPrivacy := tx.(*transaction.TxCustomTokenPrivacy)
			totalTxsFee[*txCustomPrivacy.GetTokenID()] = txCustomPrivacy.GetTxFeeToken()
		}
	}
	state.totalTxFee = totalTxsFee

	newBlock := state.newBlock
	merkleRoots := blockchain.Merkle{}.BuildMerkleTreeStore(newBlock.Body.Transactions)
	merkleRoot := &common.Hash{}
	if len(merkleRoots) > 0 {
		merkleRoot = merkleRoots[len(merkleRoots)-1]
	}
	crossTransactionRoot, err := blockchain.CreateMerkleCrossTransaction(newBlock.Body.CrossTransactions)
	if err != nil {
		return err
	}

	s2b := newBlock.CreateShardToBeaconBlock(nil)

	totalInstructions := []string{}
	for _, value := range s2b.Instructions {
		totalInstructions = append(totalInstructions, value...)
	}
	instructionsHash, err := v2.GenerateHashFromStringArray(totalInstructions)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.InstructionsHashError, err)
	}

	tempShardCommitteePubKeys, err := incognitokey.CommitteeKeyListToString(state.newView.ShardCommittee)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.UnExpectedError, err)
	}
	committeeRoot, err := v2.GenerateHashFromStringArray(tempShardCommitteePubKeys)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.CommitteeHashError, err)
	}
	tempShardPendintValidator, err := incognitokey.CommitteeKeyListToString(state.newView.ShardPendingValidator)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.UnExpectedError, err)
	}
	pendingValidatorRoot, err := v2.GenerateHashFromStringArray(tempShardPendintValidator)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.PendingValidatorRootError, err)
	}
	stakingTxRoot, err := v2.GenerateHashFromMapStringString(state.stakingTx)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.StakingTxHashError, err)
	}

	flattenInsts, err := blockchain.FlattenAndConvertStringInst(s2b.Instructions)
	if err != nil {
		return blockchain.NewBlockChainError(blockchain.FlattenAndConvertStringInstError, fmt.Errorf("Instruction from block body: %+v", err))
	}
	// TODO: check Order of instructions must be preserved in shardprocess
	instMerkleRoot := blockchain.GetKeccak256MerkleRoot(flattenInsts)
	// shard tx root
	_, shardTxMerkleData := blockchain.CreateShardTxRoot2(newBlock.Body.Transactions)

	state.newBlock.Header = ShardHeader{
		Producer:          state.proposer,
		ProducerPubKeyStr: state.proposer,

		ShardID:              shardID,
		Version:              blockchain.SHARD_BLOCK_VERSION,
		PreviousBlockHash:    *state.curView.Block.Hash(),
		Height:               state.curView.Block.GetHeight() + 1,
		Round:                1,
		TimeSlot:             state.createTimeSlot,
		Epoch:                state.newBlockEpoch,
		CrossShardBitMap:     blockchain.CreateCrossShardByteArray(state.newBlock.Body.Transactions, shardID),
		BeaconHeight:         state.newConfirmBeaconHeight,
		BeaconHash:           state.newConfirmBeaconHash,
		TotalTxsFee:          state.totalTxFee,
		ConsensusType:        common.BlsConsensus2,
		Timestamp:            state.createTimeStamp,
		TxRoot:               *merkleRoot,
		ShardTxRoot:          shardTxMerkleData[len(shardTxMerkleData)-1],
		CrossTransactionRoot: *crossTransactionRoot,
		InstructionsRoot:     instructionsHash,
		CommitteeRoot:        committeeRoot,
		PendingValidatorRoot: pendingValidatorRoot,
		StakingTxRoot:        stakingTxRoot,
	}
	copy(newBlock.Header.InstructionMerkleRoot[:], instMerkleRoot)

	state.newBlock.ConsensusHeader = ConsensusHeader{
		TimeSlot:       state.createTimeSlot,
		Proposer:       state.proposer,
		ValidationData: "",
	}

	return nil
}

func (s *CoreApp) compileBlockAndUpdateNewView(state *CreateNewBlockState) error {
	return nil
}

//==============================Validate Logic===============================
func (s *CoreApp) preValidate(state *ValidateBlockState) error {
	shardID := state.curView.ShardID
	newBlock := state.newView.GetBlock().(*ShardBlock)
	oldBlock := state.curView.GetBlock().(*ShardBlock)
	// TODO: verify proposer in timeslot
	// TODO: check block version, should be function which return version base on block height
	if newBlock.Header.Version != blockchain.SHARD_BLOCK_VERSION {
		return blockchain.NewBlockChainError(blockchain.WrongVersionError, fmt.Errorf("Expect newBlock version %+v but get %+v", 1, newBlock.Header.Version))
	}

	if state.isPreSign {
		// get beacon blocks confirmed by proposed block
		if newBlock.Header.BeaconHeight < oldBlock.Header.BeaconHeight {
			return errors.New("Beaconheight is not valid")
		}
		if newBlock.Header.BeaconHeight > oldBlock.Header.BeaconHeight {
			state.isOldBeaconHeight = true
		}
		if newBlock.Header.BeaconHeight > oldBlock.Header.BeaconHeight {
			state.isOldBeaconHeight = false
			beaconBlocks := state.bc.GetValidBeaconBlockFromPool()
			if len(beaconBlocks) == int(newBlock.Header.BeaconHeight-oldBlock.Header.BeaconHeight) {
				state.beaconBlocks = beaconBlocks
			} else {
				return errors.New("Not enough beacon to validate")
			}
		}

		// get crossshard blocks confirmed by proposed block
		allConfirmCrossShard := make(map[byte][]*CrossShardBlock)
		for _, beaconBlk := range state.beaconBlocks {
			confirmCrossShard := beaconBlk.GetConfirmedCrossShardBlockToShard()
			for fromShardID, v := range confirmCrossShard[shardID] {
				for _, crossShard := range v {
					allConfirmCrossShard[fromShardID] = append(allConfirmCrossShard[fromShardID], crossShard)
				}
			}
		}
		state.crossShardBlocks = allConfirmCrossShard
		//TODO: get crossshard from pool and check if we have enough to validate

		// get transaction confirmed by proposed block
		for _, tx := range newBlock.Body.Transactions {
			txType := tx.GetType()
			if txType == common.TxCustomTokenPrivacyType {
				state.txsToAdd = append(state.txsToAdd, tx.(*transaction.TxCustomTokenPrivacy))
			}
		}

	} else {
		//check if block has enough valid signature

	}
	return nil
}

//==============================Save Database Logic===========================
func (s *CoreApp) storeDatabase(state *StoreDatabaseState) error {

	return nil
}
