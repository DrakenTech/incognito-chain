package blockchain

import (
	"fmt"
	"time"

	"github.com/ninjadotorg/constant/common"
	"github.com/ninjadotorg/constant/metadata"
	"github.com/ninjadotorg/constant/privacy"
	"github.com/ninjadotorg/constant/transaction"
)

type BlkTmplGenerator struct {
	txPool      TxPool
	chain       *BlockChain
	rewardAgent RewardAgent
}

// txPool represents a source of transactions to consider for inclusion in
// new blocks.
//
// The interface contract requires that all of these methods are safe for
// concurrent access with respect to the source.
type TxPool interface {
	// LastUpdated returns the last time a transaction was added to or
	// removed from the source pool.
	LastUpdated() time.Time

	// MiningDescs returns a slice of mining descriptors for all the
	// transactions in the source pool.
	MiningDescs() []*metadata.TxDesc

	// HaveTransaction returns whether or not the passed transaction hash
	// exists in the source pool.
	HaveTransaction(hash *common.Hash) bool

	// RemoveTx remove tx from tx resource
	RemoveTx(tx metadata.Transaction) error

	//CheckTransactionFee
	// CheckTransactionFee(tx metadata.Transaction) (uint64, error)

	// Check tx validate by it self
	// ValidateTxByItSelf(tx metadata.Transaction) bool
}

type RewardAgent interface {
	GetBasicSalary(chainID byte) uint64
	GetSalaryPerTx(chainID byte) uint64
}

func (self BlkTmplGenerator) Init(txPool TxPool, chain *BlockChain, rewardAgent RewardAgent) (*BlkTmplGenerator, error) {
	return &BlkTmplGenerator{
		txPool:      txPool,
		chain:       chain,
		rewardAgent: rewardAgent,
	}, nil
}

func (blockgen *BlkTmplGenerator) NewBlockTemplate(payToAddress *privacy.PaymentAddress, privatekey *privacy.SpendingKey, chainID byte) (*Block, error) {

	prevBlock := blockgen.chain.BestState[chainID].BestBlock
	prevBlockHash := blockgen.chain.BestState[chainID].BestBlock.Hash()
	//prevCmTree := blockgen.chain.BestState[chainID].CmTree.MakeCopy()
	sourceTxns := blockgen.txPool.MiningDescs()

	// Get salary per tx
	salaryPerTx := blockgen.rewardAgent.GetSalaryPerTx(chainID)
	// Get basic salary on block
	basicSalary := blockgen.rewardAgent.GetBasicSalary(chainID)
	var accumulativeValues *accumulativeValues
	var buyBackFromInfos []*buyBackFromInfo
	var txGroups *txGroups
	var err error

	if len(sourceTxns) < common.MinTxsInBlock {
		// if len of sourceTxns < MinTxsInBlock -> wait for more transactions
		Logger.log.Info("not enough transactions. Wait for more...")
		<-time.Tick(common.MinBlockWaitTime * time.Second)
		sourceTxns = blockgen.txPool.MiningDescs()
		if len(sourceTxns) == 0 {
			<-time.Tick(common.MaxBlockWaitTime * time.Second)
			sourceTxns = blockgen.txPool.MiningDescs()
			if len(sourceTxns) == 0 {
				// return nil, errors.Zero("No TxNormal")
				Logger.log.Info("Creating empty block...")
				goto concludeBlock
			}
		}
	}

concludeBlock:
	txGroups, accumulativeValues, buyBackFromInfos, err = blockgen.checkAndGroupTxs(sourceTxns, chainID, privatekey)
	if err != nil {
		return nil, err
	}

	// check len of txs in block
	if len(txGroups.txsToAdd) == 0 {
		// return nil, errors.Zero("no transaction available for this chain")
		Logger.log.Info("Creating empty block...")
	}

	// Get blocksalary fund from txs
	salaryMULTP := uint64(0) //salary multiplier
	fmt.Print(txGroups)
	for _, blockTx := range txGroups.txsToAdd {
		if blockTx.GetTxFee() > 0 {
			salaryMULTP++
		}
	}

	// ------------------------ HOW to GET salary on a block-------------------
	// total salary = tx * (salary per tx) + (basic salary on block)
	// ------------------------------------------------------------------------
	totalSalary := salaryMULTP*salaryPerTx + basicSalary
	// create salary tx to pay constant for block producer
	salaryTx := new(transaction.Tx)
	err = salaryTx.InitTxSalary(totalSalary, payToAddress, privatekey, blockgen.chain.config.DataBase, nil)
	if err != nil {
		Logger.log.Error(err)
		return nil, err
	}
	accumulativeValues.totalSalary = totalSalary
	txGroups, accumulativeValues, updatedOracleValues, err := blockgen.buildResponseTxs(chainID, sourceTxns, privatekey, txGroups, accumulativeValues, buyBackFromInfos)
	if err != nil {
		Logger.log.Error(err)
		return nil, err
	}

	coinbases, err := blockgen.buildCoinbases(chainID, privatekey, txGroups, salaryTx)
	if err != nil {
		Logger.log.Error(err)
		return nil, err
	}
	txGroups.txsToAdd = append(coinbases, txGroups.txsToAdd...)

	for _, tx := range txGroups.txToRemove {
		blockgen.txPool.RemoveTx(tx)
	}

	// Check for final balance of DCB and GOV
	if accumulativeValues.currentSalaryFund+accumulativeValues.totalFee+accumulativeValues.incomeFromBonds < accumulativeValues.totalSalary+accumulativeValues.govPayoutAmount+accumulativeValues.buyBackCoins+accumulativeValues.totalRefundAmt+accumulativeValues.totalOracleRewards {
		return nil, fmt.Errorf("Gov fund is not enough for salary and dividend payout")
	}

	currentBankFund := prevBlock.Header.BankFund
	if currentBankFund < accumulativeValues.bankPayoutAmount { // Can't spend loan payment just received in this block
		return nil, fmt.Errorf("Bank fund is not enough for dividend payout")
	}

	merkleRoots := Merkle{}.BuildMerkleTreeStore(txGroups.txsToAdd)
	merkleRoot := merkleRoots[len(merkleRoots)-1]

	block := Block{
		Transactions: make([]metadata.Transaction, 0),
	}

	block.Header = BlockHeader{
		Height:        prevBlock.Header.Height + 1,
		Version:       BlockVersion,
		PrevBlockHash: *prevBlockHash,
		MerkleRoot:    *merkleRoot,
		// MerkleRootCommitments: common.Hash{},
		Timestamp:          time.Now().Unix(),
		BlockCommitteeSigs: make([]string, common.TotalValidators),
		Committee:          make([]string, common.TotalValidators),
		ChainID:            chainID,
		SalaryFund:         accumulativeValues.currentSalaryFund + accumulativeValues.incomeFromBonds + accumulativeValues.totalFee - accumulativeValues.totalSalary - accumulativeValues.govPayoutAmount - accumulativeValues.buyBackCoins - accumulativeValues.totalRefundAmt - accumulativeValues.totalOracleRewards,
		BankFund:           prevBlock.Header.BankFund + accumulativeValues.loanPaymentAmount - accumulativeValues.bankPayoutAmount,
		GOVConstitution:    prevBlock.Header.GOVConstitution, // TODO: 0xbunyip need get from gov-params tx
		DCBConstitution:    prevBlock.Header.DCBConstitution, // TODO: 0xbunyip need get from dcb-params tx
		Oracle:             prevBlock.Header.Oracle,
	}

	err = (&block).updateBlock(blockgen, txGroups, accumulativeValues, updatedOracleValues)
	if err != nil {
		Logger.log.Error(err)
		return nil, err
	}

	// Add new commitments to merkle tree and save the root
	/*newTree := prevCmTree
	err = UpdateMerkleTreeForBlock(newTree, &block)
	if err != nil {
		Logger.log.Error(err)
		return nil, err
	}
	rt = newTree.GetRoot(common.IncMerkleTreeHeight)
	copy(block.Header.MerkleRootCommitments[:], rt)*/

	return &block, nil
}
