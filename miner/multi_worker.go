package miner

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
)

type multiWorker struct {
	regularWorker    *worker
	flashbotsWorker  *worker
	megaBundleWorker *worker
}

func (w *multiWorker) stop() {
	w.regularWorker.stop()
	w.flashbotsWorker.stop()
	w.megaBundleWorker.stop()
}

func (w *multiWorker) start() {
	w.regularWorker.start()
	w.flashbotsWorker.start()
	w.megaBundleWorker.start()
}

func (w *multiWorker) close() {
	w.regularWorker.close()
	w.flashbotsWorker.close()
	w.megaBundleWorker.close()
}

func (w *multiWorker) isRunning() bool {
	return w.regularWorker.isRunning() ||
		w.flashbotsWorker.isRunning() ||
		w.megaBundleWorker.isRunning()
}

func (w *multiWorker) setExtra(extra []byte) {
	w.regularWorker.setExtra(extra)
	w.flashbotsWorker.setExtra(extra)
	w.megaBundleWorker.setExtra(extra)
}

func (w *multiWorker) setRecommitInterval(interval time.Duration) {
	w.regularWorker.setRecommitInterval(interval)
	w.flashbotsWorker.setRecommitInterval(interval)
	w.megaBundleWorker.setRecommitInterval(interval)
}

func (w *multiWorker) setEtherbase(addr common.Address) {
	w.regularWorker.setEtherbase(addr)
	w.flashbotsWorker.setEtherbase(addr)
	w.megaBundleWorker.setEtherbase(addr)
}

func (w *multiWorker) enablePreseal() {
	w.regularWorker.enablePreseal()
	w.flashbotsWorker.enablePreseal()
	w.megaBundleWorker.enablePreseal()
}

func (w *multiWorker) disablePreseal() {
	w.regularWorker.disablePreseal()
	w.flashbotsWorker.disablePreseal()
	w.megaBundleWorker.disablePreseal()
}

func newMultiWorker(config *Config, chainConfig *params.ChainConfig, engine consensus.Engine, eth Backend, mux *event.TypeMux, isLocalBlock func(*types.Block) bool, init bool) *multiWorker {
	queue := make(chan *task)

	return &multiWorker{
		regularWorker: newWorker(
			config, chainConfig, engine, eth, mux, isLocalBlock, init, &flashbotsData{
				queue: queue,
			},
		),
		flashbotsWorker: newWorker(
			config, chainConfig, engine, eth, mux, isLocalBlock, init, &flashbotsData{
				isFlashbots: true,
				queue:       queue,
			},
		),
		megaBundleWorker: newWorker(
			config, chainConfig, engine, eth, mux, isLocalBlock, init, &flashbotsData{
				isMegaBundleWorker: true,
				queue:              queue,
			},
		),
	}
}

type flashbotsData struct {
	isFlashbots        bool
	isMegaBundleWorker bool
	queue              chan *task
}
