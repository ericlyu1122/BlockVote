package evlib

import (
	"bufio"
	"bytes"
	wallet "cs.ubc.ca/cpsc416/BlockVote/Identity"
	blockChain "cs.ubc.ca/cpsc416/BlockVote/blockchain"
	"cs.ubc.ca/cpsc416/BlockVote/blockvote"
	"errors"
	"fmt"
	"github.com/DistributedClocks/tracing"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"strings"
	"time"
)

type EV struct {
	// Add EV instance state here.
	voterWallet      wallet.Wallets
	voterWalletAddr  string
	N_Receives       int
	CandidateList    []string
	minerIpPort      string
	coordIPPort      string
	localMinerIPPort string
	localCoordIPPort string
	coordClient      *rpc.Client
	minerClient      *rpc.Client
	VoterTxnMap      map[string]blockChain.Transaction
	MinerAddrList    []string
}

// create wallet for voters
// create transcation
// sign transaction
func NewEV() *EV {
	return &EV{}
}

// ----- evlib APIs -----
type VoterNameID struct {
	Name string
	ID   string
}

var quit chan bool
var voterInfo []VoterNameID

func (d *EV) connectCoord() error {
	// setup conn to coord

	lcAddr, err := net.ResolveTCPAddr("tcp", d.localCoordIPPort)
	if err != nil {
		return err
	}

	cAddr, err := net.ResolveTCPAddr("tcp", d.coordIPPort)
	if err != nil {
		return err
	}

	conn, err := net.DialTCP("tcp", lcAddr, cAddr)
	if err != nil {
		return err
	}
	d.coordClient = rpc.NewClient(conn)
	return nil
}

func (d *EV) connectMiner() error {
	// setup conn to coord
	lmAddr, err := net.ResolveTCPAddr("tcp", d.localMinerIPPort)
	if err != nil {
		return err
	}

	mAddr, err := net.ResolveTCPAddr("tcp", d.minerIpPort)
	if err != nil {
		return err
	}

	conn, err := net.DialTCP("tcp", lmAddr, mAddr)
	if err != nil {
		return err
	}
	d.minerClient = rpc.NewClient(conn)
	return nil
}

// Start Starts the instance of EV to use for connecting to the system with the given coord's IP:port.
func (d *EV) Start(localTracer *tracing.Tracer, clientId string, coordIPPort string, localCoordIPPort string, localMinerIPPort string, N_Receives int) error {
	voterInfo = make([]VoterNameID, 0)
	d.N_Receives = N_Receives
	d.coordIPPort = coordIPPort
	d.localCoordIPPort = localCoordIPPort
	d.localMinerIPPort = localMinerIPPort
	d.VoterTxnMap = make(map[string]blockChain.Transaction)

	// setup conn to coord
	for {
		err := d.connectCoord()
		if err == nil {
			break
		}
	}

	// get candidates from Coord
	var candidatesReply *blockvote.GetCandidatesReply
	for {
		err := d.connectCoord()
		err = d.coordClient.Call("CoordAPIClient.GetCandidates", blockvote.GetCandidatesArgs{}, &candidatesReply)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// print all candidates Name
	canadiateName := make([]string, 0)
	for _, cand := range candidatesReply.Candidates {
		wallets := wallet.DecodeToWallets(cand)
		canadiateName = append(canadiateName, wallets.CandidateData.CandidateName)
	}
	d.CandidateList = canadiateName
	fmt.Println("List of candidate:", canadiateName)

	quit = make(chan bool)
	go func() {
		// call coord for list of active miners with length N_Receives
		for {
			var minerListReply *blockvote.GetMinerListReply
			// TODO
			for {
				err := d.connectCoord()
				err = d.coordClient.Call("CoordAPIClient.GetMinerList", blockvote.GetMinerListArgs{}, &minerListReply)
				if err == nil && len(minerListReply.MinerAddrList) > 0 {
					break
				}
				fmt.Println("fail GetMinerList retry...")
				time.Sleep(5 * time.Second)
			}

			// random pick one miner addr
			index := 0
			d.MinerAddrList = minerListReply.MinerAddrList
			if len(d.MinerAddrList) > 1 {
				index = rand.Intn(len(d.MinerAddrList) - 1)
			}
			d.minerIpPort = d.MinerAddrList[index]
			select {
			case <-quit:
				// end
				return
			default:
				// Do other stuff
			}
		}
	}()

	// use terminal to auto or manually crate ballot
	//ballot := createBallot()
	//err = d.Vote(ballot.VoterName, ballot.VoterStudentID, ballot.VoterCandidate)
	//if err != nil {
	//	return err
	//}

	return nil
}

// helper function for checking the existence of voter
func findVoterExist(from, to string) bool {
	for _, v := range voterInfo {
		if v.Name == from && v.ID == to {
			return true
		}
	}
	return false
}

// helper function for remove minerList
func sliceMinerList(mAddr string, minerList []string) []string {
	for i, v := range minerList {
		if mAddr == v {
			minerList = append(minerList[:i], minerList[i+1:]...)
			return minerList
		}
	}
	return minerList
}

// Vote API provides the functionality of voting
func (d *EV) Vote(from, fromID, to string) error {

	ballot := blockChain.Ballot{
		VoterName:      from,
		VoterStudentID: fromID,
		VoterCandidate: to,
	}
	// create wallet for voter, only when such voter is not exist
	if !findVoterExist(from, fromID) {
		d.createVoterWallet(ballot)
		voterInfo = append(voterInfo, VoterNameID{
			Name: from,
			ID:   to,
		})
	}

	// create transaction
	txn := d.createTransaction(ballot)

	var submitTxnReply *blockvote.SubmitTxnReply
	for {
		// setup conn to miner
		err := d.connectMiner()
		err = d.minerClient.Call("MinerAPIClient.SubmitTxn", blockvote.SubmitTxnArgs{Txn: txn}, &submitTxnReply)
		if err == nil {
			d.VoterTxnMap[from] = txn
			break
		} else {
			fmt.Println("fail in SubmitTxn, retry... d.MinerAddrList len: ", len(d.MinerAddrList))
			d.MinerAddrList = sliceMinerList(d.minerIpPort, d.MinerAddrList)
			if len(d.MinerAddrList) > 0 {
				d.minerIpPort = d.MinerAddrList[rand.Intn(len(d.MinerAddrList)-1)]
			} else {
				var minerListReply *blockvote.GetMinerListReply
				for {
					err := d.connectCoord()
					err = d.coordClient.Call("CoordAPIClient.GetMinerList", blockvote.GetMinerListArgs{}, &minerListReply)
					if err == nil && len(d.MinerAddrList) > 0 {
						if minerListReply != nil {
							d.MinerAddrList = minerListReply.MinerAddrList
							if len(d.MinerAddrList) > 0 {
								break
							}
						}
					}
					fmt.Println("fail GetMinerList in SubmitTxn, retry... ")
					time.Sleep(5 * time.Second)
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (d *EV) submitTxn(txn blockChain.Transaction) error {

	var submitTxnReply *blockvote.SubmitTxnReply
	for {
		// setup conn to miner
		err := d.connectMiner()
		err = d.minerClient.Call("MinerAPIClient.SubmitTxn", blockvote.SubmitTxnArgs{Txn: txn}, &submitTxnReply)
		if err == nil {
			break
		} else {
			fmt.Println("fail in SubmitTxn, retry... d.MinerAddrList len: ", len(d.MinerAddrList))
			d.MinerAddrList = sliceMinerList(d.minerIpPort, d.MinerAddrList)
			if len(d.MinerAddrList) > 0 {
				d.minerIpPort = d.MinerAddrList[rand.Intn(len(d.MinerAddrList)-1)]
			} else {
				var minerListReply *blockvote.GetMinerListReply
				for {
					err := d.connectCoord()
					err = d.coordClient.Call("CoordAPIClient.GetMinerList", blockvote.GetMinerListArgs{}, &minerListReply)
					d.MinerAddrList = minerListReply.MinerAddrList
					if err == nil {
						break
					}
					fmt.Println("d.MinerAddrList len: ", len(d.MinerAddrList))
					time.Sleep(500 * time.Millisecond)
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

//func findTxn(TxID []byte, voterTxnMap map[string]blockChain.Transaction) blockChain.Transaction {
//	for _, txn := range voterTxnMap {
//		if bytes.Compare(TxID, txn.ID) == 0 {
//			return txn
//		}
//	}
//	return nil
//}

// GetBallotStatus API checks the status of a transaction and returns the number of blocks that confirm it
func (d *EV) GetBallotStatus(TxID []byte) (int, error) {
	result := -1
	retry := 0
	var queryTxnReply *blockvote.QueryTxnReply
	for {
		err := d.connectCoord()
		err = d.coordClient.Call("CoordAPIClient.QueryTxn", blockvote.QueryTxnArgs{
			TxID: TxID,
		}, &queryTxnReply)
		if err == nil {
			if queryTxnReply != nil {
				result = queryTxnReply.NumConfirmed
				if result != -1 {
					break
				}
			}
		}
		fmt.Println("fail to queryTxn, retry...")
		retry++
		if retry == 3 {
			tempTxn := blockChain.Transaction{
				Data:      nil,
				ID:        nil,
				Signature: nil,
				PublicKey: nil,
			}
			for _, txn := range d.VoterTxnMap {
				if bytes.Compare(TxID, txn.ID) == 0 {
					tempTxn = txn
				}
			}
			fmt.Println("fail to queryTxn, resubmit submitTxn...", tempTxn.ID)
			d.submitTxn(tempTxn)
			retry = 0
		}
		time.Sleep(30 * time.Second)
	}
	return result, nil
}

// GetCandVotes API retrieve the number of votes a candidate has.
func (d *EV) GetCandVotes(candidate string) (uint, error) {
	if len(d.CandidateList) == 0 {
		return 0, errors.New("Empty Candidates.\n")
	}
	var queryResultReply *blockvote.QueryResultsReply
	for {
		err := d.connectCoord()
		err = d.coordClient.Call("CoordAPIClient.QueryResults", blockvote.QueryResultsArgs{}, &queryResultReply)
		if err == nil {
			break
		}
		fmt.Println("fail to QueryResults, retry...")
		time.Sleep(10 * time.Second)
	}

	idx := 0
	fmt.Println(queryResultReply)
	for i, cand := range d.CandidateList {
		if cand == candidate {
			idx = i
		}
	}
	return queryResultReply.Votes[idx], nil
}

// Stop Stops the EV instance.
// This call always succeeds.
func (d *EV) Stop() {
	quit <- true
	d.coordClient.Close()
	d.minerClient.Close()
	return
}

// ----- evlib utility functions -----

func createBallot() blockChain.Ballot {
	// enter ballot
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter your name: ")
	voterName, _ := reader.ReadString('\n')

	reader = bufio.NewReader(os.Stdin)
	fmt.Print("Enter your studentID: ")
	voterId, _ := reader.ReadString('\n')
	// ^[0-9]{8}
	isIDValid := false
	candidateName := ""
	for !isIDValid {
		reader = bufio.NewReader(os.Stdin)
		fmt.Print("Vote your vote Candidate: ")
		candidateName, _ = reader.ReadString('\n')
	}

	ballot := blockChain.Ballot{
		strings.TrimRight(voterName, "\r\n"),
		strings.TrimRight(voterId, "\r\n"),
		strings.TrimRight(candidateName, "\r\n"),
	}
	return ballot
}

func (d *EV) createVoterWallet(ballot blockChain.Ballot) {
	v, err := wallet.CreateVoter(ballot.VoterName, ballot.VoterStudentID)
	if err != nil {
		log.Panic(err)
	}
	d.voterWallet = *v
	addr := d.voterWallet.AddWallet()
	d.voterWalletAddr = addr
	d.voterWallet.SaveFile()
}

func (d *EV) createTransaction(ballot blockChain.Ballot) blockChain.Transaction {
	txn := blockChain.Transaction{
		Data:      &ballot,
		ID:        nil,
		Signature: nil,
		PublicKey: d.voterWallet.Wallets[d.voterWalletAddr].PublicKey,
	}
	// client sign with private key
	txn.Sign(d.voterWallet.Wallets[d.voterWalletAddr].PrivateKey)
	return txn
}

//Client - Coord Interaction
//Clients need to contact coord before they issue transactions
//or when they check the status of the transactions.
//Before issuing a transaction, a client should retrieve a list of
//active miners from coord to select miners to send the transaction.
//However, a client should not contact coord whenever it wants to
//issue a transaction. To check the status of a transaction, clients
//should send the query to coord, and the coord will use its local
//copy of the blockchain to return the result.
//
//Client - Miner Interaction
//After receiving a list of active miners from coord, the client will
//select N_RECEIVERS miners to submit its transaction. When the client
//cannot find its transaction after a set timeout, it will resubmit the
//same transaction again.
