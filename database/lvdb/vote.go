package lvdb

import (
	"encoding/binary"
	"sort"

	"github.com/ninjadotorg/constant/common"
	"github.com/ninjadotorg/constant/database"
	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

func (db *db) AddVoteBoard(boardType string, boardIndex uint32, paymentAddress []byte, VoterPubKey []byte, CandidatePubKey []byte, amount uint64) error {
	//add to sum amount of vote token to this candidate
	key := GetKeyVoteBoardSum(boardType, boardIndex, CandidatePubKey)
	ok, err := db.HasValue(key)
	if err != nil {
		return err
	}
	if !ok {
		zeroInBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(zeroInBytes, uint64(0))
		db.Put(key, zeroInBytes)
	}

	currentVoteInBytes, err := db.lvdb.Get(key, nil)
	currentVote := binary.LittleEndian.Uint64(currentVoteInBytes)
	newVote := currentVote + amount

	newVoteInBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(newVoteInBytes, newVote)
	err = db.Put(key, newVoteInBytes)
	if err != nil {
		return database.NewDatabaseError(database.UnexpectedError, errors.Wrap(err, "db.lvdb.put"))
	}

	// add to count amount of vote to this candidate
	key = GetKeyVoteBoardCount(boardType, boardIndex, CandidatePubKey)
	currentCountInBytes, err := db.lvdb.Get(key, nil)
	if err != nil {
		return err
	}
	currentCount := binary.LittleEndian.Uint32(currentCountInBytes)
	newCount := currentCount + 1
	newCountInByte := make([]byte, 4)
	binary.LittleEndian.PutUint32(newCountInByte, newCount)
	err = db.Put(key, newCountInByte)
	if err != nil {
		return database.NewDatabaseError(database.UnexpectedError, errors.Wrap(err, "db.lvdb.put"))
	}

	// add to list voter new voter base on count as index
	key = GetKeyVoteBoardList(boardType, boardIndex, CandidatePubKey, VoterPubKey)
	oldAmountInByte, _ := db.Get(key)
	oldAmount := ParseValueVoteBoardList(oldAmountInByte)
	newAmount := oldAmount + amount
	newAmountInByte := GetValueVoteBoardList(newAmount)
	err = db.Put(key, newAmountInByte)

	//add database to get paymentAddress from pubKey
	key = GetPubKeyToPaymentAddressKey(VoterPubKey)
	db.Put(key, paymentAddress)

	return nil
}

func GetNumberOfGovernor(boardType string) int {
	numberOfGovernors := common.NumberOfDCBGovernors
	if boardType == "gov" {
		numberOfGovernors = common.NumberOfGOVGovernors
	}
	return numberOfGovernors
}

func (db *db) GetTopMostVoteGovernor(boardType string, currentBoardIndex uint32) (database.CandidateList, error) {
	var candidateList database.CandidateList
	//use prefix  as in file lvdb/block.go FetchChain
	newBoardIndex := currentBoardIndex + 1
	prefix := GetKeyVoteBoardSum(boardType, newBoardIndex, make([]byte, 0))
	iter := db.lvdb.NewIterator(util.BytesPrefix(prefix), nil)
	for iter.Next() {
		_, _, pubKey, err := ParseKeyVoteBoardSum(iter.Key())
		countKey := GetKeyVoteBoardCount(boardType, newBoardIndex, pubKey)
		if err != nil {
			return nil, err
		}
		countValue, err := db.Get(countKey)
		if err != nil {
			return nil, err
		}
		value := binary.LittleEndian.Uint64(iter.Value())
		candidateList = append(candidateList, database.CandidateElement{
			PubKey:       pubKey,
			VoteAmount:   value,
			NumberOfVote: common.BytesToUint32(countValue),
		})
	}
	sort.Sort(candidateList)
	numberOfGovernors := GetNumberOfGovernor(boardType)
	if len(candidateList) < numberOfGovernors {
		return nil, database.NewDatabaseError(database.NotEnoughCandidate, errors.Errorf("not enough Candidate"))
	}

	return candidateList[len(candidateList)-numberOfGovernors:], nil
}

func (db *db) NewIterator(slice *util.Range, ro *opt.ReadOptions) iterator.Iterator {
	return db.lvdb.NewIterator(slice, ro)
}

func (db *db) AddVoteLv3Proposal(boardType string, constitutionIndex uint32, txID *common.Hash) error {
	//init sealer
	keySealer := GetKeyThreePhraseCryptoSealer(boardType, constitutionIndex, txID)
	ok, err := db.HasValue(keySealer)
	if err != nil {
		return err
	}
	if ok {
		return errors.Errorf("duplicate txid")
	}
	zeroInBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(zeroInBytes, 0)
	db.Put(keySealer, zeroInBytes)

	// init owner
	keyOwner := GetKeyThreePhraseCryptoOwner(boardType, constitutionIndex, txID)
	ok, err = db.HasValue(keyOwner)
	if err != nil {
		return err
	}
	if ok {
		return errors.Errorf("duplicate txid")
	}
	db.Put(keyOwner, zeroInBytes)

	return nil
}

func (db *db) AddVoteLv1or2Proposal(boardType string, constitutionIndex uint32, txID *common.Hash) error {
	keySealer := GetKeyThreePhraseCryptoSealer(boardType, constitutionIndex, txID)
	ok, err := db.HasValue(keySealer)
	if err != nil {
		return err
	}
	if ok {
		return errors.Errorf("duplicate txid")
	}
	valueInBytes, err := db.Get(keySealer)
	if err != nil {
		return err
	}
	value := binary.LittleEndian.Uint32(valueInBytes)
	newValue := value + 1
	newValueInByte := make([]byte, 4)
	binary.LittleEndian.PutUint32(newValueInByte, newValue)
	db.Put(keySealer, newValueInByte)
	return nil
}

func (db *db) AddVoteNormalProposalFromSealer(boardType string, constitutionIndex uint32, txID *common.Hash, voteValue []byte) error {
	err := db.AddVoteLv1or2Proposal(boardType, constitutionIndex, txID)
	if err != nil {
		return err
	}
	key := GetKeyThreePhraseVoteValue(boardType, constitutionIndex, txID)

	db.Put(key, voteValue)

	return nil
}

func (db *db) AddVoteNormalProposalFromOwner(boardType string, constitutionIndex uint32, txID *common.Hash, voteValue []byte) error {
	keyOwner := GetKeyThreePhraseCryptoOwner(boardType, constitutionIndex, txID)
	ok, err := db.HasValue(keyOwner)
	if err != nil {
		return err
	}
	if ok {
		return errors.Errorf("duplicate txid")
	}
	if err != nil {
		return err
	}
	newValueInByte := common.Uint32ToBytes(1)
	db.Put(keyOwner, newValueInByte)

	key := GetKeyThreePhraseVoteValue(boardType, constitutionIndex, txID)
	db.Put(key, voteValue)

	return nil
}

func GetPubKeyToPaymentAddressKey(pubKey []byte) []byte {
	key := append(pubKeyToPaymentAddress, pubKey...)
	return key
}

func GetPosFromLength(length []int) []int {
	pos := []int{0}
	for i := 0; i < len(length); i++ {
		pos = append(pos, pos[i]+length[i])
	}
	return pos
}

func CheckLength(key []byte, length []int) bool {
	return len(key) != length[len(length)-1]
}

func GetKeyFromVariadic(args ...[]byte) []byte {
	length := make([]int, 0)
	for i := 0; i < len(args); i++ {
		length = append(length, len(args[i]))
	}
	pos := GetPosFromLength(length)
	key := make([]byte, pos[len(pos)-1])
	for i := 0; i < len(pos)-1; i++ {
		copy(key[pos[i]:pos[i+1]], args[i])
	}
	return key
}

func ParseKeyToSlice(key []byte, length []int) (error, [][]byte) {
	pos := GetPosFromLength(length)
	if pos[len(pos)-1] != len(key) {
		return errors.New("key and length of args not match"), nil
	}
	res := make([][]byte, 0)
	for i := 0; i < len(pos)-1; i++ {
		res = append(res, key[pos[i]:pos[i+1]])
	}
	return nil, res
}

func GetKeyVoteBoardSum(boardType string, boardIndex uint32, candidatePubKey []byte) []byte {
	key := GetKeyFromVariadic(voteBoardSumPrefix, []byte(boardType), common.Uint32ToBytes(boardIndex), candidatePubKey)
	return key
}

func ParseKeyVoteBoardSum(key []byte) (boardType string, boardIndex uint32, candidatePubKey []byte, err error) {
	length := []int{len(voteBoardSumPrefix), 3, 4, common.PubKeyLength}
	err, elements := ParseKeyToSlice(key, length)

	_ = elements[0]
	boardType = string(elements[1])
	boardIndex = common.BytesToUint32(elements[2])
	candidatePubKey = elements[3]
	return
}

func GetKeyVoteBoardCount(boardType string, boardIndex uint32, candidatePubKey []byte) []byte {
	key := GetKeyFromVariadic(voteBoardCountPrefix, []byte(boardType), common.Uint32ToBytes(boardIndex), candidatePubKey)
	return key
}

func ParseKeyVoteBoardCount(key []byte) (boardType string, boardIndex uint32, candidatePubKey []byte, err error) {
	length := []int{len(voteBoardCountPrefix), 3, 4, common.PubKeyLength}
	err, elements := ParseKeyToSlice(key, length)

	_ = elements[0]
	boardType = string(elements[1])
	boardIndex = common.BytesToUint32(elements[2])
	candidatePubKey = elements[3]
	err = nil
	return
}

func GetKeyVoteBoardList(boardType string, boardIndex uint32, candidatePubKey []byte, voterPubKey []byte) []byte {
	key := GetKeyFromVariadic(voteBoardListPrefix, []byte(boardType), common.Uint32ToBytes(boardIndex), candidatePubKey, voterPubKey)
	return key
}

func ParseKeyVoteBoardList(key []byte) (boardType string, boardIndex uint32, candidatePubKey []byte, voterPubKey []byte, err error) {
	length := []int{len(voteBoardListPrefix), 3, 4, common.PubKeyLength, common.PubKeyLength}
	err, elements := ParseKeyToSlice(key, length)

	_ = elements[0]
	boardType = string(elements[1])
	boardIndex = common.BytesToUint32(elements[2])
	candidatePubKey = elements[3]
	voterPubKey = elements[4]
	err = nil
	return
}

func GetValueVoteBoardList(amount uint64) []byte {
	return common.Uint64ToBytes(amount)
}

func ParseValueVoteBoardList(value []byte) uint64 {
	return common.BytesToUint64(value)
}

func GetKeyVoteTokenAmount(boardType string, boardIndex uint32, pubKey []byte) []byte {
	key := GetKeyFromVariadic(VoteTokenAmountPrefix, []byte(boardType), common.Uint32ToBytes(boardIndex), pubKey)
	return key
}

func (db *db) GetVoteTokenAmount(boardType string, boardIndex uint32, pubKey []byte) (uint32, error) {
	key := GetKeyVoteTokenAmount(boardType, boardIndex, pubKey)
	value, err := db.Get(key)
	if err != nil {
		return 0, err
	}
	return common.BytesToUint32(value), nil
}

func GetKeyThreePhraseCryptoOwner(boardType string, constitutionIndex uint32, txId *common.Hash) []byte {
	txIdByte := make([]byte, 0)
	if txId != nil {
		txIdByte = txId.GetBytes()
	}
	key := GetKeyFromVariadic(threePhraseCryptoOwnerPrefix, []byte(boardType), common.Uint32ToBytes(constitutionIndex), txIdByte)
	return key
}

func ParseKeyThreePhraseCryptoOwner(key []byte) (boardType string, constitutionIndex uint32, txId *common.Hash, err error) {
	length := []int{len(threePhraseCryptoOwnerPrefix), 3, 4, common.PubKeyLength}
	if CheckLength(key, length) {
		length[len(length)-1] = 0
	}
	err, elements := ParseKeyToSlice(key, length)

	_ = elements[0]
	boardType = string(elements[1])
	constitutionIndex = common.BytesToUint32(elements[2])

	txId = nil
	txIdData := elements[3]
	if len(txIdData) != 0 {
		newHash := common.NewHash(txIdData)
		txId = &newHash
	}

	err = nil
	return
}

func ParseValueThreePhraseCryptoOwner(value []byte) (uint32, error) {
	i := common.BytesToUint32(value)
	return i, nil
}

func GetKeyThreePhraseCryptoSealer(boardType string, constitutionIndex uint32, txId *common.Hash) []byte {
	txIdByte := make([]byte, 0)
	if txId != nil {
		txIdByte = txId.GetBytes()
	}
	key := GetKeyFromVariadic(threePhraseCryptoSealerPrefix, []byte(boardType), common.Uint32ToBytes(constitutionIndex), txIdByte)
	return key
}

func ParseKeyThreePhraseCryptoSealer(key []byte) (boardType string, constitutionIndex uint32, txId *common.Hash, err error) {
	length := []int{len(threePhraseCryptoSealerPrefix), 3, 4, common.PubKeyLength}
	if CheckLength(key, length) {
		length[len(length)-1] = 0
	}
	err, elements := ParseKeyToSlice(key, length)

	boardType = string(elements[0])
	constitutionIndex = common.BytesToUint32(elements[1])

	txId = nil
	txIdData := elements[2]
	if len(txIdData) != 0 {
		newHash := common.NewHash(txIdData)
		txId = &newHash
	}
	return
}

func GetKeyWinningVoter(boardType string, constitutionIndex uint32) []byte {
	key := GetKeyFromVariadic(winningVoterPrefix, []byte(boardType), common.Uint32ToBytes(constitutionIndex))
	return key
}

func GetKeyThreePhraseVoteValue(boardType string, constitutionIndex uint32, txId *common.Hash) []byte {
	txIdByte := make([]byte, 0)
	if txId != nil {
		txIdByte = txId.GetBytes()
	}
	key := GetKeyFromVariadic(threePhraseVoteValuePrefix, []byte(boardType), common.Uint32ToBytes(constitutionIndex), txIdByte)
	return key
}

func ParseKeyThreePhraseVoteValue(key []byte) (boardType string, constitutionIndex uint32, txId *common.Hash, err error) {
	length := []int{len(threePhraseVoteValuePrefix), 3, 4, common.PubKeyLength}
	if CheckLength(key, length) {
		length[len(length)-1] = 0
	}
	err, elements := ParseKeyToSlice(key, length)

	boardType = string(elements[0])
	constitutionIndex = common.BytesToUint32(elements[1])

	txId = nil
	txIdData := elements[2]
	if len(txIdData) != 0 {
		newHash := common.NewHash(txIdData)
		txId = &newHash
	}
	return
}

func ParseValueThreePhraseVoteValue(value []byte) (*common.Hash, int32, error) {
	txId := common.NewHash(value[:common.HashSize])
	amount := common.BytesToInt32(value[common.HashSize:])
	return &txId, amount, nil
}

func GetKeyEncryptFlag(boardType string) []byte {
	key := GetKeyFromVariadic(encryptFlagPrefix, []byte(boardType))
	return key
}

func (db *db) GetEncryptFlag(boardType string) uint32 {
	key := GetKeyEncryptFlag(boardType)
	value, _ := db.Get(key)
	return common.BytesToUint32(value)
}

func (db *db) SetEncryptFlag(boardType string, flag uint32) {
	key := GetKeyEncryptFlag(boardType)
	value := common.Uint32ToBytes(flag)
	db.Put(key, value)
}

func GetKeyEncryptionLastBlockHeight(boardType string) []byte {
	key := GetKeyFromVariadic(encryptionLastBlockHeightPrefix, []byte(boardType))
	return key
}

func (db *db) GetEncryptionLastBlockHeight(boardType string) uint32 {
	key := GetKeyEncryptionLastBlockHeight(boardType)
	value, _ := db.Get(key)
	return common.BytesToUint32(value)
}

func (db *db) SetEncryptionLastBlockHeight(boardType string, height uint32) {
	key := GetKeyEncryptionLastBlockHeight(boardType)
	value := common.Uint32ToBytes(height)
	db.Put(key, value)
}

func (db *db) GetAmountVoteToken(boardType string, boardIndex uint32, pubKey []byte) (uint32, error) {
	key := GetKeyVoteTokenAmount(boardType, boardIndex, pubKey)
	currentAmountInBytes, err := db.Get(key)
	if err != nil {
		return 0, err
	}
	currentAmount := common.BytesToUint32(currentAmountInBytes)
	return currentAmount, nil
}

func (db *db) TakeVoteTokenFromWinner(boardType string, boardIndex uint32, voterPubKey []byte, amountOfVote int32) error {
	key := GetKeyVoteTokenAmount(boardType, boardIndex, voterPubKey)
	currentAmountInByte, err := db.Get(key)
	if err != nil {
		return err
	}
	currentAmount := common.BytesToUint32(currentAmountInByte)
	newAmount := currentAmount - uint32(amountOfVote)
	db.Put(key, common.Uint32ToBytes(newAmount))
	return nil
}

func (db *db) SetNewProposalWinningVoter(boardType string, constitutionIndex uint32, voterPubKey []byte) error {
	key := GetKeyWinningVoter(boardType, constitutionIndex)
	db.Put(key, voterPubKey)
	return nil
}

func (db *db) GetPaymentAddressFromPubKey(pubKey []byte) []byte {
	key := GetPubKeyToPaymentAddressKey(pubKey)
	value, _ := db.Get(key)
	return value
}

func (db *db) GetBoardVoterList(boardType string, candidatePubKey []byte, boardIndex uint32) [][]byte {
	begin := GetKeyVoteBoardList(boardType, boardIndex, candidatePubKey, make([]byte, common.PubKeyLength))
	end := GetKeyVoteBoardList(boardType, boardIndex, common.BytesPlusOne(candidatePubKey), make([]byte, common.PubKeyLength))
	searchRange := util.Range{
		Start: begin,
		Limit: end,
	}

	iter := db.NewIterator(&searchRange, nil)
	listVoter := make([][]byte, 0)
	for iter.Next() {
		key := iter.Key()
		_, _, _, pubKey, _ := ParseKeyVoteBoardList(key)
		listVoter = append(listVoter, pubKey)
	}
	return listVoter
}
