// Copyright 2021 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/state/snapshot"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/triedb"
)

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (bc *BlockChain) CurrentHeader() *types.Header {
	return bc.hc.CurrentHeader()
}

// CurrentBlock retrieves the current head block of the canonical chain. The
// block is retrieved from the blockchain's internal cache.
func (bc *BlockChain) CurrentBlock() *types.Header {
	return bc.currentBlock.Load()
}

// CurrentSnapBlock retrieves the current snap-sync head block of the canonical
// chain. The block is retrieved from the blockchain's internal cache.
func (bc *BlockChain) CurrentSnapBlock() *types.Header {
	return bc.currentSnapBlock.Load()
}

// CurrentFinalBlock retrieves the current finalized block of the canonical
// chain. The block is retrieved from the blockchain's internal cache.
func (bc *BlockChain) CurrentFinalBlock() *types.Header {
	if p, ok := bc.engine.(consensus.PoSA); ok {
		currentHeader := bc.CurrentHeader()
		if currentHeader == nil {
			return nil
		}
		return p.GetFinalizedHeader(bc, currentHeader)
	}

	return nil
}

// CurrentSafeBlock retrieves the current safe block of the canonical
// chain. The block is retrieved from the blockchain's internal cache.
func (bc *BlockChain) CurrentSafeBlock() *types.Header {
	if p, ok := bc.engine.(consensus.PoSA); ok {
		currentHeader := bc.CurrentHeader()
		if currentHeader == nil {
			return nil
		}
		_, justifiedBlockHash, err := p.GetJustifiedNumberAndHash(bc, []*types.Header{currentHeader})
		if err == nil {
			return bc.GetHeaderByHash(justifiedBlockHash)
		}
	}
	return nil
}

// HasHeader checks if a block header is present in the database or not, caching
// it if present.
func (bc *BlockChain) HasHeader(hash common.Hash, number uint64) bool {
	return bc.hc.HasHeader(hash, number)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (bc *BlockChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	return bc.hc.GetHeader(hash, number)
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (bc *BlockChain) GetHeaderByHash(hash common.Hash) *types.Header {
	return bc.hc.GetHeaderByHash(hash)
}

// GetVerifiedBlockByHash retrieves the header of a verified block, it may be only in memory.
func (bc *BlockChain) GetVerifiedBlockByHash(hash common.Hash) *types.Header {
	highestVerifiedBlock := bc.highestVerifiedBlock.Load()
	if highestVerifiedBlock != nil && highestVerifiedBlock.Hash() == hash {
		return highestVerifiedBlock
	}
	return bc.hc.GetHeaderByHash(hash)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (bc *BlockChain) GetHeaderByNumber(number uint64) *types.Header {
	return bc.hc.GetHeaderByNumber(number)
}

// GetHeadersFrom returns a contiguous segment of headers, in rlp-form, going
// backwards from the given number.
func (bc *BlockChain) GetHeadersFrom(number, count uint64) []rlp.RawValue {
	return bc.hc.GetHeadersFrom(number, count)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (bc *BlockChain) GetBody(hash common.Hash) *types.Body {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := bc.bodyCache.Get(hash); ok {
		return cached
	}
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBody(bc.db, hash, *number)
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	bc.bodyCache.Add(hash, body)
	return body
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (bc *BlockChain) GetBodyRLP(hash common.Hash) rlp.RawValue {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := bc.bodyRLPCache.Get(hash); ok {
		return cached
	}
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBodyRLP(bc.db, hash, *number)
	if len(body) == 0 {
		return nil
	}
	// Cache the found body for next time and return
	bc.bodyRLPCache.Add(hash, body)
	return body
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BlockChain) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	if !bc.HasHeader(hash, number) {
		return false
	}
	return rawdb.HasBody(bc.db, hash, number)
}

// HasFastBlock checks if a fast block is fully present in the database or not.
func (bc *BlockChain) HasFastBlock(hash common.Hash, number uint64) bool {
	if !bc.HasBlock(hash, number) {
		return false
	}
	if bc.receiptsCache.Contains(hash) {
		return true
	}
	return rawdb.HasReceipts(bc.db, hash, number)
}

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (bc *BlockChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if block, ok := bc.blockCache.Get(hash); ok {
		return block
	}
	block := rawdb.ReadBlock(bc.db, hash, number)
	if block == nil {
		return nil
	}
	// Cache the found block for next time and return
	bc.blockCache.Add(block.Hash(), block)
	return block
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (bc *BlockChain) GetBlockByHash(hash common.Hash) *types.Block {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return bc.GetBlock(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (bc *BlockChain) GetBlockByNumber(number uint64) *types.Block {
	hash := rawdb.ReadCanonicalHash(bc.db, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return bc.GetBlock(hash, number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (bc *BlockChain) GetBlocksFromHash(hash common.Hash, n int) (blocks []*types.Block) {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := bc.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		*number--
	}
	return
}

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (bc *BlockChain) GetReceiptsByHash(hash common.Hash) types.Receipts {
	if receipts, ok := bc.receiptsCache.Get(hash); ok {
		return receipts
	}
	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number == nil {
		return nil
	}
	header := bc.GetHeader(hash, *number)
	if header == nil {
		return nil
	}
	receipts := rawdb.ReadReceipts(bc.db, hash, *number, header.Time, bc.chainConfig)
	if receipts == nil {
		return nil
	}
	bc.receiptsCache.Add(hash, receipts)
	return receipts
}

// GetSidecarsByHash retrieves the sidecars for all transactions in a given block.
func (bc *BlockChain) GetSidecarsByHash(hash common.Hash) types.BlobSidecars {
	if sidecars, ok := bc.sidecarsCache.Get(hash); ok {
		return sidecars
	}
	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number == nil {
		return nil
	}
	sidecars := rawdb.ReadBlobSidecars(bc.db, hash, *number)
	if sidecars == nil {
		return nil
	}
	bc.sidecarsCache.Add(hash, sidecars)
	return sidecars
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (bc *BlockChain) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	uncles := []*types.Header{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = bc.GetBlock(block.ParentHash(), block.NumberU64()-1)
	}
	return uncles
}

// GetCanonicalHash returns the canonical hash for a given block number
func (bc *BlockChain) GetCanonicalHash(number uint64) common.Hash {
	return bc.hc.GetCanonicalHash(number)
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (bc *BlockChain) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	return bc.hc.GetAncestor(hash, number, ancestor, maxNonCanonical)
}

// GetTransactionLookup retrieves the lookup along with the transaction
// itself associate with the given transaction hash.
//
// An error will be returned if the transaction is not found, and background
// indexing for transactions is still in progress. The transaction might be
// reachable shortly once it's indexed.
//
// A null will be returned in the transaction is not found and background
// transaction indexing is already finished. The transaction is not existent
// from the node's perspective.
func (bc *BlockChain) GetTransactionLookup(hash common.Hash) (*rawdb.LegacyTxLookupEntry, *types.Transaction, error) {
	bc.txLookupLock.RLock()
	defer bc.txLookupLock.RUnlock()

	// Short circuit if the txlookup already in the cache, retrieve otherwise
	if item, exist := bc.txLookupCache.Get(hash); exist {
		return item.lookup, item.transaction, nil
	}
	tx, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(bc.db, hash)
	if tx == nil {
		progress, err := bc.TxIndexProgress()
		if err != nil {
			// No error is returned if the transaction indexing progress is unreachable
			// due to unexpected internal errors. In such cases, it is impossible to
			// determine whether the transaction does not exist or has simply not been
			// indexed yet without a progress marker.
			//
			// In such scenarios, the transaction is treated as unreachable, though
			// this is clearly an unintended and unexpected situation.
			return nil, nil, nil
		}
		// The transaction indexing is not finished yet, returning an
		// error to explicitly indicate it.
		if !progress.Done() {
			return nil, nil, errors.New("transaction indexing still in progress")
		}
		// The transaction is already indexed, the transaction is either
		// not existent or not in the range of index, returning null.
		return nil, nil, nil
	}
	lookup := &rawdb.LegacyTxLookupEntry{
		BlockHash:  blockHash,
		BlockIndex: blockNumber,
		Index:      txIndex,
	}
	bc.txLookupCache.Add(hash, txLookup{
		lookup:      lookup,
		transaction: tx,
	})
	return lookup, tx, nil
}

// GetTd retrieves a block's total difficulty in the canonical chain from the
// database by hash and number, caching it if found.
func (bc *BlockChain) GetTd(hash common.Hash, number uint64) *big.Int {
	return bc.hc.GetTd(hash, number)
}

// HasState checks if state trie is fully present in the database or not.
func (bc *BlockChain) HasState(hash common.Hash) bool {
	if bc.NoTries() {
		if bc.snaps != nil {
			return bc.snaps.Snapshot(hash) != nil
		}
		// snaps is nil when the blockchain creates
		found, err := snapshot.PreCheckSnapshot(bc.db, hash)
		if err != nil {
			log.Warn("Check HasState in NoTries mode failed", "root", hash, "err", err)
			return false
		}
		return found
	}
	_, err := bc.statedb.OpenTrie(hash)
	return err == nil
}

// HasBlockAndState checks if a block and associated state trie is fully present
// in the database or not, caching it if present.
func (bc *BlockChain) HasBlockAndState(hash common.Hash, number uint64) bool {
	// Check first that the block itself is known
	block := bc.GetBlock(hash, number)
	if block == nil {
		return false
	}
	return bc.HasState(block.Root())
}

// stateRecoverable checks if the specified state is recoverable.
// Note, this function assumes the state is not present, because
// state is not treated as recoverable if it's available, thus
// false will be returned in this case.
func (bc *BlockChain) stateRecoverable(root common.Hash) bool {
	if bc.triedb.Scheme() == rawdb.HashScheme {
		return false
	}
	result, _ := bc.triedb.Recoverable(root)
	return result
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash either from ephemeral in-memory cache, or from persistent storage.
func (bc *BlockChain) ContractCodeWithPrefix(hash common.Hash) []byte {
	// TODO(rjl493456442) The associated account address is also required
	// in Verkle scheme. Fix it once snap-sync is supported for Verkle.
	return bc.statedb.ContractCodeWithPrefix(common.Address{}, hash)
}

// State returns a new mutable state based on the current HEAD block.
func (bc *BlockChain) State() (*state.StateDB, error) {
	return bc.StateAt(bc.CurrentBlock().Root)
}

// StateAt returns a new mutable state based on a particular point in time.
func (bc *BlockChain) StateAt(root common.Hash) (*state.StateDB, error) {
	stateDb, err := state.New(root, bc.statedb)
	if err != nil {
		return nil, err
	}

	// If there's no trie and the specified snapshot is not available, getting
	// any state will by default return nil.
	// Instead of that, it will be more useful to return an error to indicate
	// the state is not available.
	if stateDb.NoTrie() && stateDb.GetSnap() == nil {
		return nil, errors.New("state is not available")
	}

	return stateDb, err
}

// Config retrieves the chain's fork configuration.
func (bc *BlockChain) Config() *params.ChainConfig { return bc.chainConfig }

// Engine retrieves the blockchain's consensus engine.
func (bc *BlockChain) Engine() consensus.Engine { return bc.engine }

// Snapshots returns the blockchain snapshot tree.
func (bc *BlockChain) Snapshots() *snapshot.Tree {
	return bc.snaps
}

// Validator returns the current validator.
func (bc *BlockChain) Validator() Validator {
	return bc.validator
}

// Processor returns the current processor.
func (bc *BlockChain) Processor() Processor {
	return bc.processor
}

// StateCache returns the caching database underpinning the blockchain instance.
func (bc *BlockChain) StateCache() state.Database {
	return bc.statedb
}

// GasLimit returns the gas limit of the current HEAD block.
func (bc *BlockChain) GasLimit() uint64 {
	return bc.CurrentBlock().GasLimit
}

// Genesis retrieves the chain's genesis block.
func (bc *BlockChain) Genesis() *types.Block {
	return bc.genesisBlock
}

// GenesisHeader retrieves the chain's genesis block header.
func (bc *BlockChain) GenesisHeader() *types.Header {
	return bc.genesisBlock.Header()
}

// TxIndexProgress returns the transaction indexing progress.
func (bc *BlockChain) TxIndexProgress() (TxIndexProgress, error) {
	if bc.txIndexer == nil {
		return TxIndexProgress{}, errors.New("tx indexer is not enabled")
	}
	return bc.txIndexer.txIndexProgress()
}

// TrieDB retrieves the low level trie database used for data storage.
func (bc *BlockChain) TrieDB() *triedb.Database {
	return bc.triedb
}

// HeaderChain returns the underlying header chain.
func (bc *BlockChain) HeaderChain() *HeaderChain {
	return bc.hc
}

// SubscribeRemovedLogsEvent registers a subscription of RemovedLogsEvent.
func (bc *BlockChain) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return bc.scope.Track(bc.rmLogsFeed.Subscribe(ch))
}

// SubscribeChainEvent registers a subscription of ChainEvent.
func (bc *BlockChain) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return bc.scope.Track(bc.chainFeed.Subscribe(ch))
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (bc *BlockChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return bc.scope.Track(bc.chainHeadFeed.Subscribe(ch))
}

// SubscribeHighestVerifiedHeaderEvent registers a subscription of HighestVerifiedBlockEvent.
func (bc *BlockChain) SubscribeHighestVerifiedHeaderEvent(ch chan<- HighestVerifiedBlockEvent) event.Subscription {
	return bc.scope.Track(bc.highestVerifiedBlockFeed.Subscribe(ch))
}

// SubscribeChainBlockEvent registers a subscription of ChainBlockEvent.
func (bc *BlockChain) SubscribeChainBlockEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return bc.scope.Track(bc.chainBlockFeed.Subscribe(ch))
}

// SubscribeLogsEvent registers a subscription of []*types.Log.
func (bc *BlockChain) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return bc.scope.Track(bc.logsFeed.Subscribe(ch))
}

// SubscribeBlockProcessingEvent registers a subscription of bool where true means
// block processing has started while false means it has stopped.
func (bc *BlockChain) SubscribeBlockProcessingEvent(ch chan<- bool) event.Subscription {
	return bc.scope.Track(bc.blockProcFeed.Subscribe(ch))
}

// SubscribeFinalizedHeaderEvent registers a subscription of FinalizedHeaderEvent.
func (bc *BlockChain) SubscribeFinalizedHeaderEvent(ch chan<- FinalizedHeaderEvent) event.Subscription {
	return bc.scope.Track(bc.finalizedHeaderFeed.Subscribe(ch))
}

// AncientTail retrieves the tail the ancients blocks
func (bc *BlockChain) AncientTail() (uint64, error) {
	tail, err := bc.db.BlockStore().Tail()
	if err != nil {
		return 0, err
	}
	return tail, nil
}
