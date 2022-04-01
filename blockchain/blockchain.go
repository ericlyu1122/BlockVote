package blockchain

import (
	"bytes"
	"crypto/ecdsa"
	"cs.ubc.ca/cpsc416/BlockVote/util"
	"encoding/hex"
	"errors"
	"log"
	"math"
)

var LastHashKey = []byte("LastHash")

const BlockKeyPrefix = "block-"

type BlockChain struct {
	LastHash []byte
	DB       *util.Database
}

type ChainIterator struct {
	LastHash    []byte
	CurrentHash []byte
	Index       int
	BlockChain  *BlockChain
}

// ----- BlockChain APIs -----

func NewBlockChain(DB *util.Database) *BlockChain {
	return &BlockChain{DB: DB}
}

// Init initializes the blockchain with genesis block. For coord use only.
func (bc *BlockChain) Init() error {
	// check key
	if bc.DB.KeyExist(LastHashKey) {
		return errors.New("blockchain has already been initialized")
	}

	// generate genesis block
	genesis := Block{}
	genesis.Genesis()

	// store genesis block
	err := bc.DB.PutMulti(
		[][]byte{DBKeyForBlock(genesis.Hash), LastHashKey},
		[][]byte{genesis.Encode(), genesis.Hash})
	if err != nil {
		return err
	}

	// update last hash
	bc.LastHash = genesis.Hash
	return nil
}

// ResumeFromDB resumes a blockchain from database. For coord use only.
func (bc *BlockChain) ResumeFromDB() error {
	lastHash, err := bc.DB.Get(LastHashKey)
	if err != nil {
		return err
	}

	// update last hash
	bc.LastHash = lastHash
	return nil
}

// ResumeFromEncodedData resumes a blockchain from byte data. For miner use only.
func (bc *BlockChain) ResumeFromEncodedData(blocks [][]byte, lastHash []byte) error {
	// save last hash & every block to DB
	// (all blocks are assumed valid)
	var keys [][]byte
	for _, blockBytes := range blocks {
		block := DecodeToBlock(blockBytes)
		keys = append(keys, DBKeyForBlock(block.Hash))
	}
	keys = append(keys, LastHashKey)
	values := append(blocks, lastHash)
	err := bc.DB.PutMulti(keys, values)
	if err != nil {
		return err
	}

	// update last hash
	bc.LastHash = lastHash
	return nil
}

// Encode encodes all the blocks in the blockchain into a 2D byte array.
func (bc *BlockChain) Encode() ([][]byte, []byte) {
	blocks, err := bc.DB.GetAllWithPrefix(BlockKeyPrefix)
	if err != nil {
		log.Println("[ERROR] Unable to fetch all block data from database:")
		log.Fatal(err)
	}
	return blocks, bc.LastHash
}

// Exist returns if a block exists in the blockchain
func (bc *BlockChain) Exist(hash []byte) bool {
	key := DBKeyForBlock(hash)
	return bc.DB.KeyExist(key)
}

// Get gets a block by hash
func (bc *BlockChain) Get(hash []byte) *Block {
	data, err := bc.DB.Get(DBKeyForBlock(hash))
	if err != nil {
		log.Println("[ERROR] Unable to fetch the block from DB:")
		log.Fatal(err)
	}
	block := DecodeToBlock(data)
	return block
}

// Put adds a new block to the blockchain
func (bc *BlockChain) Put(block Block, owned bool) (success bool) {
	// sanity check
	if len(block.PrevHash) == 0 || block.BlockNum == 0 || len(block.Hash) == 0 || len(block.MinerID) == 0 {
		log.Println("[WARN] Block has missing values and will not be added to the chain.")
		return false
	}
	if !bc.Exist(block.PrevHash) {
		log.Println("[WARN] Previous block does not exist and the block will not be added to the chain.")
		return false
	}
	if bc.Exist(block.Hash) {
		log.Println("[WARN] Block already exists and will not be added to the chain.")
		return false
	}

	// validate
	if !owned {
		// TODO: Add block validation code here
		// validate pow
		pow := NewProof(&block)
		if !pow.Validate() {
			return false
		}
		// validate txns

	}

	// save to db
	err := bc.DB.Put(DBKeyForBlock(block.Hash), block.Encode())
	if err != nil {
		log.Println("[ERROR] Unable to save the block:")
		log.Fatal(err)
	}

	// check chain
	if bytes.Compare(block.PrevHash, bc.LastHash) == 0 {
		bc.LastHash = block.Hash
	}
	return true
}

// CheckoutFork checks out a different fork and returns any difference between two forks
func (bc *BlockChain) CheckoutFork(lastHashNew []byte) (newTxns []*Transaction, oldTxns []*Transaction) {
	if bytes.Compare(lastHashNew, bc.LastHash) == 0 {
		log.Println("[WARN] Attempting to checkout the same fork")
		return
	}

	iterNew, iterOld := bc.NewIterator(lastHashNew), bc.NewIterator(bc.LastHash)
	var blockHashesNew [][]byte
	var blockHashesOld [][]byte

	// collect all block hashes
	for block, end := iterNew.Next(); !end; block, end = iterNew.Next() {
		blockHashesNew = append([][]byte{block.Hash}, blockHashesNew...)
	}
	for block, end := iterOld.Next(); !end; block, end = iterOld.Next() {
		blockHashesOld = append([][]byte{block.Hash}, blockHashesOld...)
	}

	// find first different
	i := 0
	for ; i < int(math.Min(float64(len(blockHashesNew)), float64(len(blockHashesOld)))); i++ {
		if bytes.Compare(blockHashesNew[i], blockHashesOld[i]) != 0 {
			break
		}
	}

	// collect txns
	for _, hash := range blockHashesNew[i:] {
		block := bc.Get(hash)
		for _, txn := range block.Txns {
			newTxns = append(newTxns, txn)
		}
	}
	for _, hash := range blockHashesOld[i:] {
		block := bc.Get(hash)
		for _, txn := range block.Txns {
			oldTxns = append(oldTxns, txn)
		}
	}

	return newTxns, oldTxns
}

// NewIterator returns a chain iterator
func (bc *BlockChain) NewIterator(hash []byte) *ChainIterator {
	return &ChainIterator{
		LastHash:    hash,
		CurrentHash: hash,
		Index:       -1,
		BlockChain:  bc,
	}
}

// TxnStatus returns the number of blocks that confirm the given txn. -1 indicates txn not found
func (bc *BlockChain) TxnStatus(txid []byte) int {
	// get an iterator for the longest chain
	iter := bc.NewIterator(bc.LastHash)
	res := -1
	for block, end := iter.Next(); !end; block, end = iter.Next() {
		for _, txn := range block.Txns {
			if bytes.Compare(txn.ID, txid) == 0 {
				res = iter.Index
				break
			}
		}
		if res != -1 {
			break
		}
	}

	return res
}

// ----- ChainIterator APIs -----

func (iter *ChainIterator) Next() (block *Block, end bool) {
	block = iter.BlockChain.Get(iter.CurrentHash)
	iter.CurrentHash = block.PrevHash
	iter.Index++
	return block, block.BlockNum == 0
}

func (iter *ChainIterator) Reset() {
	iter.CurrentHash = iter.LastHash
	iter.Index = -1
}

// ----- Utility functions -----

// DBKeyForBlock returns the database key for a given block hash by concatenating prefix and hash.
func DBKeyForBlock(blockHash []byte) []byte {
	return bytes.Join([][]byte{[]byte(BlockKeyPrefix), blockHash}, []byte{})
}

// TODO: need to merge below function for transaction use
func (bc *BlockChain) FindTransaction(ID []byte) (Transaction, error) {

	iter := bc.NewIterator(bc.LastHash)

	for {
		block, _ := iter.Next()

		for _, tx := range block.Txns {
			if bytes.Compare(tx.ID, ID) == 0 {
				return *tx, nil
			}
		}

		if len(block.PrevHash) == 0 {
			break
		}
	}

	return Transaction{}, errors.New("Transaction does not exist")
}

func (bc *BlockChain) SignTransaction(tx *Transaction, privKey ecdsa.PrivateKey) {
	prevTXs := make(map[string]Transaction)

	for _, in := range tx.Inputs {
		prevTX, err := bc.FindTransaction(in.ID)
		if err != nil {
			log.Panic(err)
		}
		prevTXs[hex.EncodeToString(prevTX.ID)] = prevTX
	}

	tx.Sign(privKey, prevTXs)
}

func (bc *BlockChain) VerifyTransaction(tx *Transaction) bool {
	prevTXs := make(map[string]Transaction)

	for _, in := range tx.Inputs {
		prevTX, err := bc.FindTransaction(in.ID)

		if err != nil {
			log.Panic(err)
		}

		prevTXs[hex.EncodeToString(prevTX.ID)] = prevTX
	}
	return tx.Verify(prevTXs)

	return false
}

func (bc *BlockChain) FindSpendableOutputs(pubKeyHash []byte, amount int) (int, map[string][]int) {
	unspentOuts := make(map[string][]int)
	unspentTxs := bc.FindUnspentTransactions(pubKeyHash)
	accumulated := 0

Work:
	for _, tx := range unspentTxs {
		txID := hex.EncodeToString(tx.ID)

		for outIdx, out := range tx.Outputs {
			if out.IsLockedWithKey(pubKeyHash) && accumulated < amount {
				accumulated += out.Value
				unspentOuts[txID] = append(unspentOuts[txID], outIdx)

				if accumulated >= amount {
					break Work
				}
			}
		}
	}
	return accumulated, unspentOuts
}

func (bc *BlockChain) FindUnspentTransactions(pubKeyHash []byte) []Transaction {
	var unspentTxs []Transaction

	spentTXOs := make(map[string][]int)

	iter := bc.NewIterator(bc.LastHash)

	for {
		block, _ := iter.Next()

		for _, tx := range block.Txns {
			txID := hex.EncodeToString(tx.ID)

		Outputs:
			for outIdx, out := range tx.Outputs {
				if spentTXOs[txID] != nil {
					for _, spentOut := range spentTXOs[txID] {
						if spentOut == outIdx {
							continue Outputs
						}
					}
				}
				if out.IsLockedWithKey(pubKeyHash) {
					unspentTxs = append(unspentTxs, *tx)
				}
			}
		}

		if len(block.PrevHash) == 0 {
			break
		}
	}
	return unspentTxs
}
