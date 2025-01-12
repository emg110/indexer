package importer

import (
	"bytes"
	"fmt"

	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-algorand/rpcs"

	"github.com/algorand/indexer/idb"
)

// Importer is used to import blocks into an idb.IndexerDb object.
type Importer interface {
	ImportBlock(blockbytes []byte) (txCount int, err error)
	ImportDecodedBlock(block *rpcs.EncodedBlockCert) (txCount int, err error)
}

type dbImporter struct {
	db idb.IndexerDb
}

var zeroAddr = [32]byte{}

func participate(participants [][]byte, addr []byte) [][]byte {
	if len(addr) == 0 || bytes.Equal(addr, zeroAddr[:]) {
		return participants
	}
	for _, pp := range participants {
		if bytes.Equal(pp, addr) {
			return participants
		}
	}
	return append(participants, addr)
}

// ImportBlock processes a block and adds it to the IndexerDb
func (imp *dbImporter) ImportBlock(blockbytes []byte) (txCount int, err error) {
	var blockContainer rpcs.EncodedBlockCert
	err = protocol.Decode(blockbytes, &blockContainer)
	if err != nil {
		return txCount, fmt.Errorf("error decoding blockbytes, %v", err)
	}
	return imp.ImportDecodedBlock(&blockContainer)
}

// ImportBlock processes a block and adds it to the IndexerDb
func (imp *dbImporter) ImportDecodedBlock(blockContainer *rpcs.EncodedBlockCert) (txCount int, err error) {
	txCount = 0
	err = imp.db.StartBlock()
	if err != nil {
		return txCount, fmt.Errorf("error starting block, %v", err)
	}
	block := blockContainer.Block
	round := uint64(block.Round())
	for intra := range block.Payset {
		stxn, ad, err := block.BlockHeader.DecodeSignedTxn(block.Payset[intra])
		if err != nil {
			return txCount, fmt.Errorf("error decoding signed txn in block err: %w", err)
		}
		txtypeenum, ok := idb.GetTypeEnum(stxn.Txn.Type)
		if !ok {
			return txCount,
				fmt.Errorf("%d:%d unknown txn type %v", round, intra, stxn.Txn.Type)
		}
		assetid := uint64(0)
		switch txtypeenum {
		case 3:
			assetid = uint64(stxn.Txn.ConfigAsset)
			if assetid == 0 {
				assetid = block.TxnCounter - uint64(len(block.Payset)) + uint64(intra) + 1
			}
		case 4:
			assetid = uint64(stxn.Txn.XferAsset)
		case 5:
			assetid = uint64(stxn.Txn.FreezeAsset)
		case 6:
			assetid = uint64(stxn.Txn.ApplicationID)
			if assetid == 0 {
				assetid = block.TxnCounter - uint64(len(block.Payset)) + uint64(intra) + 1
			}
		}
		stxnad := transactions.SignedTxnWithAD{
			SignedTxn: stxn,
			ApplyData: ad,
		}
		participants := make([][]byte, 0, 10)
		participants = participate(participants, stxn.Txn.Sender[:])
		participants = participate(participants, stxn.Txn.Receiver[:])
		participants = participate(participants, stxn.Txn.CloseRemainderTo[:])
		participants = participate(participants, stxn.Txn.AssetSender[:])
		participants = participate(participants, stxn.Txn.AssetReceiver[:])
		participants = participate(participants, stxn.Txn.AssetCloseTo[:])
		participants = participate(participants, stxn.Txn.FreezeAccount[:])
		err = imp.db.AddTransaction(
			round, intra, int(txtypeenum), assetid, stxnad, participants)
		if err != nil {
			return txCount, fmt.Errorf("error importing txn r=%d i=%d, %v", round, intra, err)
		}
		txCount++
	}
	blockheaderBytes := protocol.Encode(&block.BlockHeader)
	err = imp.db.CommitBlock(round, block.TimeStamp, block.RewardsLevel, blockheaderBytes)
	if err != nil {
		return txCount, fmt.Errorf("error committing block, %v", err)
	}
	return
}

// NewDBImporter creates a new importer object.
func NewDBImporter(db idb.IndexerDb) Importer {
	return &dbImporter{db: db}
}
