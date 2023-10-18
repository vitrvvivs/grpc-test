package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	beacon "github.com/oasisprotocol/oasis-core/go/beacon/api"
	oasisGrpc "github.com/oasisprotocol/oasis-core/go/common/grpc"
	consensus "github.com/oasisprotocol/oasis-core/go/consensus/api"
	registry "github.com/oasisprotocol/oasis-core/go/registry/api"
	roothash "github.com/oasisprotocol/oasis-core/go/roothash/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Fix TLS missing
	//certfile, _ := ioutil.ReadFile("cert.pem")
	certPool, err := x509.SystemCertPool()
	if err != nil || certPool == nil {
		certPool = x509.NewCertPool()
	}
	//certPool.AppendCertsFromPEM(certfile)
	creds := credentials.NewTLS(&tls.Config{
		//MinVersion: tls.VersionTLS12,
		RootCAs: certPool,
		//ServerName: "grpc.cobalt.archive.emerald.oasiscloud.io",
	})

	certPool, err = x509.SystemCertPool()
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	conn, err := oasisGrpc.Dial(os.Args[1], dialOpts...)
	if err != nil {
		fmt.Print("Dial error: ")
		fmt.Println(err)
		return
	}

	beaconClient := beacon.NewBeaconClient(conn)
	consensusClient := consensus.NewConsensusClient(conn)
	roothashClient := roothash.NewRootHashClient(conn)
	registryClient := registry.NewRegistryClient(conn)
	ctx := context.Background()

	epoch, err := beaconClient.GetBaseEpoch(ctx)
	if err != nil {
		fmt.Print("GetBaseEpoch error: ")
		fmt.Println(err)
		return
	}
	fmt.Println("BaseEpoch: ", epoch)

	block, err := consensusClient.GetBlock(ctx, consensus.HeightLatest)
	height := block.Height
	fmt.Println("LatestHeight: ", height)

	runtimes, err := registryClient.GetRuntimes(ctx, &registry.GetRuntimesQuery{Height: height, IncludeSuspended: false})
	if err != nil {
		fmt.Print("GetRuntimes error: ")
		fmt.Println(err)
		return
	}
	fmt.Println("Runtimes:")

	for _, runtime := range runtimes {
		fmt.Print("\t", runtime.ID.Hex())
		runtimeState, err := roothashClient.GetRuntimeState(ctx, &roothash.RuntimeRequest{RuntimeID: runtime.ID, Height: height})
		if err != nil {
			fmt.Print("\nGetRuntimeState error: ")
			fmt.Println(err)
			continue
		}
		t := time.Unix(int64(runtimeState.CurrentBlock.Header.Timestamp), 0)
		fmt.Println("\t", t)
	}

	chainContext, err := consensusClient.GetChainContext(ctx)
	if err != nil {
		fmt.Print("GetChainContext error: ")
		fmt.Println(err)
	}
	fmt.Println("ChainContext: ", chainContext)
}
