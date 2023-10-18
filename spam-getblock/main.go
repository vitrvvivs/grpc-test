package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/oasisprotocol/oasis-core/go/common"
	"github.com/oasisprotocol/oasis-core/go/common/cbor"
	oasisGrpc "github.com/oasisprotocol/oasis-core/go/common/grpc"
	"github.com/oasisprotocol/oasis-core/go/roothash/api/block"
	runtime "github.com/oasisprotocol/oasis-core/go/runtime/client/api"
	"github.com/oasisprotocol/oasis-sdk/client-sdk/go/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	nexusRuntime "github.com/oasisprotocol/nexus/analyzer/runtime"
	"github.com/oasisprotocol/nexus/log"
	"github.com/oasisprotocol/nexus/storage/oasis/nodeapi"
)

// semi-consts; set once at startup
var (
	URL string
	NUM_REQUESTS int
	DELAY time.Duration
	TIMEOUT time.Duration

	dialOpts []grpc.DialOption
)

func SetupGrpcOpts() {
	certPool, err := x509.SystemCertPool()
	if err != nil || certPool == nil {
		certPool = x509.NewCertPool()
	}
	creds := credentials.NewTLS(&tls.Config{
		RootCAs: certPool,
	})
	dialOpts = []grpc.DialOption{grpc.WithTransportCredentials(creds)}
}

func main() {
	flag.StringVar(&URL, "url", "grpc.oasiscloud.io:443", "grpc endpoint")
	flag.IntVar(&NUM_REQUESTS, "n", 1, "number of requests")
	flag.DurationVar(&DELAY, "delay", 0*time.Millisecond, "delay between requests")
	flag.DurationVar(&TIMEOUT, "timeout", 60*time.Second, "timeout for each request")
	flag.Parse()

	start := time.Now()
	SetupGrpcOpts()
	num_errors := CallSimultaneous(
		context.Background(),
		GetSapphireRound,
		RandomSapphireHeight,
	)
	time_taken := (time.Now().Sub(start))

	fmt.Println("Total time:", time_taken)
	fmt.Println("Errors:", num_errors, "/", NUM_REQUESTS)
	fmt.Println("Rate:", float32(NUM_REQUESTS) / float32(time_taken.Seconds()), "/s")
}

type ThreadStatus struct {
	ID uint64 // height
	err error
	msg string
	times ApiTimes
}

type ApiTimes struct {
	Connect time.Duration
	GetBlock time.Duration
	GetTransactions time.Duration
	GetEvents time.Duration
	Parse time.Duration
}

func (t *ApiTimes) String() string {
	return fmt.Sprintf("Connect: %s, GetBlock: %s, GetTransactions: %s, GetEvents: %s, ExtractRound: %s",
	                   t.Connect.String(), t.GetBlock.String(), t.GetTransactions.String(), t.GetEvents.String(), t.Parse.String())
}

// returns number of failed requests
func CallSimultaneous(ctx context.Context,
					  call_f func(context.Context, uint64) ThreadStatus,
					  parameter_f func() uint64,
				     ) (num_errors int) {
	wg := sync.WaitGroup{}
	ch := make(chan ThreadStatus, NUM_REQUESTS)

	// start threads
	for i := 0; i < NUM_REQUESTS; i++ {
		subctx, cancel := context.WithTimeout(ctx, TIMEOUT)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- call_f(subctx, parameter_f())
			cancel()
		}()
		time.Sleep(DELAY)
	}

	// wait for them to finish
	wg.Wait()

	// print statuses
	for i := 0; i < NUM_REQUESTS; i++ {
		status, _ := <- ch
		// if loglevel=timing
		fmt.Println(status.times.String())
		// if loglevel=blockdata
		fmt.Println(status.msg)
		if status.err != nil {
			fmt.Printf("thread %d: %s\n", status.ID, status.err)
			num_errors += 1
		}
	}
	return num_errors
}

func RandomSapphireHeight() uint64 { // 500_000 to 900_000
	return 500_000 + (rand.Uint64() % 400_000)
}

func GetSapphireRound(ctx context.Context, height uint64) ThreadStatus {
	status := ThreadStatus{ID: height, times: ApiTimes{}}
	start := time.Now()
	conn, err := oasisGrpc.Dial(URL, dialOpts...)
	if err != nil {
		status.err = err
		return status
	}

	client := runtime.NewRuntimeClient(conn)
	sapphire := &common.Namespace{}
	sapphire.UnmarshalText(([]byte)("000000000000000000000000000000000000000000000000f80306c9858e7279"))
	status.times.Connect = time.Since(start)

	start = time.Now()
	getBlockRequest := &runtime.GetBlockRequest{
		RuntimeID: *sapphire,
		Round: height,
	}
	block, err := client.GetBlock(ctx, getBlockRequest)
	if err != nil {
		status.err = err
		return status
	}
	status.times.GetBlock = time.Since(start)

	start = time.Now()
	getTransactionsRequest := &runtime.GetTransactionsRequest{
		RuntimeID: *sapphire,
		Round: height,
	}
	txs, err := client.GetTransactionsWithResults(ctx, getTransactionsRequest)
	if err != nil {
		return status
	}
	status.times.GetTransactions = time.Since(start)

	start = time.Now()
	getEventsRequest := &runtime.GetEventsRequest{
		RuntimeID: *sapphire,
		Round: height,
	}
	events, err := client.GetEvents(ctx, getEventsRequest)
	if err != nil {
		return status
	}
	status.times.GetEvents = time.Since(start)

	start = time.Now()
	bd, err := TryNexusParseBlock(block, txs, events)
	status.msg = fmt.Sprintf("Round: %d, NumTransactions: %d, Hash: %s", bd.Header.Round, bd.NumTransactions, bd.Header.Hash)
	status.times.Parse = time.Since(start)
	return status
}

func TryNexusParseBlock(block *block.Block, blockTxs []*runtime.TransactionWithResults, blockEvents []*runtime.Event) (*nexusRuntime.BlockData, error) {
	header := nodeapi.RuntimeBlockHeader{
		Version:        block.Header.Version,
		Namespace:      block.Header.Namespace,
		Round:          block.Header.Round,
		Timestamp:      time.Unix(int64(block.Header.Timestamp), 0 /* nanos */),
		Hash:           block.Header.EncodedHash(),
		PreviousHash:   block.Header.PreviousHash,
		IORoot:         block.Header.IORoot,
		StateRoot:      block.Header.StateRoot,
		MessagesHash:   block.Header.MessagesHash,
		InMessagesHash: block.Header.InMessagesHash,
	}
	txs := make([]nodeapi.RuntimeTransactionWithResults, len(blockTxs))
	for i, tx := range blockTxs {
		nexus_tx := nodeapi.RuntimeTransactionWithResults{}

		cbor.Unmarshal(tx.Tx, &nexus_tx.Tx)
		cbor.Unmarshal(tx.Result, &nexus_tx.Result)
		for _, tx_ev := range tx.Events {
			var ev types.Event
			err := ev.UnmarshalRaw(tx_ev.Key, tx_ev.Value, nil)
			if err != nil {
				continue
			}
			nexus_tx.Events = append(nexus_tx.Events, &ev)
		}
		txs[i] = nexus_tx
	}

	//evs := make([]*types.Event, len(rawEvs))
	events := make([]nodeapi.RuntimeEvent, len(blockEvents))
	for i, rawEv := range blockEvents {
		var ev types.Event
		if err := ev.UnmarshalRaw(rawEv.Key, rawEv.Value, &rawEv.TxHash); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event '%v': %w", rawEv, err)
		}
		events[i] = (nodeapi.RuntimeEvent)(ev)
	}

	logger, _ := log.NewLogger("nexus", io.Discard, log.FmtLogfmt, log.LevelDebug)

	return nexusRuntime.ExtractRound(header, txs, events, logger)
}
