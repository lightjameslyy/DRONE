package worker

import (
	"algorithm"
	"bufio"
	"fmt"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"graph"
	"io"
	"log"
	"math"
	"net"
	"os"
	pb "protobuf"
	"strconv"
	"strings"
	"sync"
	"time"
	"tools"
)

func Generate(g graph.Graph) (map[graph.ID]int64, map[graph.ID]int64) {
	distance := make(map[graph.ID]int64)
	exchangeMsg := make(map[graph.ID]int64)

	for id := range g.GetNodes() {
		distance[id] = math.MaxInt64
	}

	for id := range g.GetFOs() {
		exchangeMsg[id] = math.MaxInt64
	}
	return distance, exchangeMsg
}

type Worker struct {
	mutex *sync.Mutex

	peers        []string
	selfId       int // the id of this worker itself in workers
	grpcHandlers []*grpc.ClientConn

	g           graph.Graph
	distance    map[graph.ID]int64 //
	exchangeMsg map[graph.ID]int64
	updated     []*algorithm.Pair

	routeTable map[graph.ID][]*algorithm.BoundMsg

	iterationNum int
	stopChannel  chan bool
}

func (w *Worker) Lock() {
	w.mutex.Lock()
}

func (w *Worker) UnLock() {
	w.mutex.Unlock()
}

// rpc send has max size limit, so we spilt our transfer into many small block
func Peer2PeerSSSPSend(client pb.WorkerClient, message []*pb.SSSPMessageStruct, wg *sync.WaitGroup)  {

	for len(message) > tools.RPCSendSize {
		slice := message[0:tools.RPCSendSize]
		message = message[tools.RPCSendSize:]
		_, err := client.SSSPSend(context.Background(), &pb.SSSPMessageRequest{Pair: slice})
		if err != nil {
			log.Println("send error")
			log.Fatal(err)
		}
	}
	if len(message) != 0 {
		_, err := client.SSSPSend(context.Background(), &pb.SSSPMessageRequest{Pair: message})
		if err != nil {
			log.Println("send error")
			log.Fatal(err)
		}
	}
	wg.Done()
}

func (w *Worker) ShutDown(ctx context.Context, args *pb.ShutDownRequest) (*pb.ShutDownResponse, error) {
	log.Println("receive shutDown request")
	w.Lock()
	defer w.Lock()
	log.Println("shutdown ing")

	for i, handle := range w.grpcHandlers {
		if i == 0 || i == w.selfId {
			continue
		}
		handle.Close()
	}
	w.stopChannel <- true
	log.Println("shutdown ok")
	return &pb.ShutDownResponse{IterationNum: int32(w.iterationNum)}, nil
}

func (w *Worker) PEval(ctx context.Context, args *pb.PEvalRequest) (*pb.PEvalResponse, error) {
	// init grpc handler and store it
	// cause until now, we can ensure all workers have in work,
	// so we do this here

	w.grpcHandlers = make([]*grpc.ClientConn, len(w.peers))
	for id, peer := range w.peers {
		if id == w.selfId || id == 0 {
			continue
		}
		conn, err := grpc.Dial(peer, grpc.WithInsecure())
		if err != nil {
			panic(err)
		}
		w.grpcHandlers[id] = conn
	}

	// Load graph data
	var fullSendStart time.Time
	var fullSendDuration float64
	var SlicePeerSend []*pb.WorkerCommunicationSize
	isMessageToSend, messages, iterationTime, combineTime, iterationNum, updatePairNum, dstPartitionNum := algorithm.SSSP_PEVal(w.g, w.distance, w.exchangeMsg, w.routeTable, graph.StringID(1))
	if !isMessageToSend {
		var SlicePeerSendNull []*pb.WorkerCommunicationSize // this struct only for hold place. contains nothing, client end should ignore it
		return &pb.PEvalResponse{Ok: isMessageToSend, Body: &pb.PEvalResponseBody{iterationNum, iterationTime,
			combineTime, updatePairNum, dstPartitionNum, 0, SlicePeerSendNull}}, nil
	} else {
		fullSendStart = time.Now()
		var wg sync.WaitGroup
		for partitionId, message := range messages {
			client := pb.NewWorkerClient(w.grpcHandlers[partitionId+1])
			encodeMessage := make([]*pb.SSSPMessageStruct, 0)
			eachWorkerCommunicationSize := &pb.WorkerCommunicationSize{int32(partitionId), int32(len(message))}
			SlicePeerSend = append(SlicePeerSend, eachWorkerCommunicationSize)
			for _, msg := range message {
				encodeMessage = append(encodeMessage, &pb.SSSPMessageStruct{NodeID: msg.NodeId.IntVal(), Distance: msg.Distance})
				//log.Printf("nodeId:%v dis:%v \n", msg.NodeId.String(), msg.Distance)
			}
			//log.Printf("send partition id:%v\n", partitionId)
			wg.Add(1)
			go Peer2PeerSSSPSend(client, encodeMessage, &wg)
		}
		wg.Wait()
		fullSendDuration = time.Since(fullSendStart).Seconds()
	}
	return &pb.PEvalResponse{Ok: isMessageToSend, Body: &pb.PEvalResponseBody{IterationNum: iterationNum, IterationSeconds: iterationTime,
		CombineSeconds: combineTime, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: fullSendDuration, PairNum: SlicePeerSend}}, nil
}

func (w *Worker) IncEval(ctx context.Context, args *pb.IncEvalRequest) (*pb.IncEvalResponse, error) {
	w.iterationNum++
	isMessageToSend, messages, iterationTime, combineTime, iterationNum, updatePairNum, dstPartitionNum, aggregateTime,
		aggregatorOriSize, aggregatorReducedSize := algorithm.SSSP_IncEval(w.g, w.distance, w.exchangeMsg, w.routeTable, w.updated)
	w.updated = make([]*algorithm.Pair, 0)
	var fullSendStart time.Time
	var fullSendDuration float64
	var SlicePeerSend []*pb.WorkerCommunicationSize
	fullSendStart = time.Now()
	if !isMessageToSend {
		var SlicePeerSendNull []*pb.WorkerCommunicationSize // this struct only for hold place, contains nothing
		return &pb.IncEvalResponse{Update: isMessageToSend, Body: &pb.IncEvalResponseBody{AggregatorOriSize: aggregatorOriSize,
			AggregatorSeconds: aggregateTime, AggregatorReducedSize: aggregatorReducedSize, IterationSeconds: iterationTime,
			CombineSeconds: combineTime, IterationNum: iterationNum, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: 0,
			PairNum: SlicePeerSendNull}}, nil
	} else {
		var wg sync.WaitGroup
		for partitionId, message := range messages {
			client := pb.NewWorkerClient(w.grpcHandlers[partitionId+1])
			encodeMessage := make([]*pb.SSSPMessageStruct, 0)
			eachWorkerCommunicationSize := &pb.WorkerCommunicationSize{int32(partitionId), int32(len(message))}
			SlicePeerSend = append(SlicePeerSend, eachWorkerCommunicationSize)
			for _, msg := range message {
				encodeMessage = append(encodeMessage, &pb.SSSPMessageStruct{NodeID: msg.NodeId.IntVal(), Distance: msg.Distance})
			}
			wg.Add(1)
			go Peer2PeerSSSPSend(client, encodeMessage, &wg)
		}
		wg.Wait()
	}
	fullSendDuration = time.Since(fullSendStart).Seconds()

	return &pb.IncEvalResponse{Update: isMessageToSend, Body: &pb.IncEvalResponseBody{AggregatorOriSize: aggregatorOriSize,
		AggregatorSeconds: aggregateTime, AggregatorReducedSize: aggregatorReducedSize, IterationSeconds: iterationTime,
		CombineSeconds: combineTime, IterationNum: iterationNum, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: fullSendDuration,
		PairNum: SlicePeerSend}}, nil
}

func (w *Worker) Assemble(ctx context.Context, args *pb.AssembleRequest) (*pb.AssembleResponse, error) {
	f, err:= os.Create(tools.ResultPath + "result_" + strconv.Itoa(w.selfId - 1))
	if err != nil {
		log.Panic(err)
	}
	writer := bufio.NewWriter(f)
	defer writer.Flush()
	defer f.Close()

	for id, dist := range w.distance {
		writer.WriteString(id.String()+"\t"+strconv.FormatInt(dist, 10) + "\n")
	}

	return &pb.AssembleResponse{Ok: true}, nil
}

func (w *Worker) SSSPSend(ctx context.Context, args *pb.SSSPMessageRequest) (*pb.SSSPMessageResponse, error) {
	log.Println("send receive")
	decodeMessage := make([]*algorithm.Pair, 0)

	for _, msg := range args.Pair {
		decodeMessage = append(decodeMessage, &algorithm.Pair{NodeId: graph.StringID(msg.NodeID), Distance: msg.Distance})
		//log.Printf("received msg: nodeId:%v dis:%v\n", graph.StringID(msg.NodeID), msg.Distance)
	}
	w.Lock()
	w.updated = append(w.updated, decodeMessage...)
	w.UnLock()

	return &pb.SSSPMessageResponse{}, nil
}

func (w *Worker) SimSend(ctx context.Context, args *pb.SimMessageRequest) (*pb.SimMessageResponse, error) {
	return nil, nil
}
func (w *Worker) PRSend(ctx context.Context, args *pb.PRMessageRequest) (*pb.PRMessageResponse, error) {
	return nil, nil
}

func newWorker(id, partitionNum int) *Worker {
	w := new(Worker)
	w.mutex = new(sync.Mutex)
	w.selfId = id
	w.peers = make([]string, 0)
	w.updated = make([]*algorithm.Pair, 0)
	w.iterationNum = 0
	w.stopChannel = make(chan bool)

	// read config file get ip:port config
	// in config file, every line in this format: id,ip:port\n
	// while id means the id of this worker, and 0 means master
	// the id of first line must be 0 (so the first ip:port is master)
	f, err := os.Open(tools.ConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadString('\n')
		line = strings.Split(line, "\n")[0] //delete the end "\n"
		if err != nil || io.EOF == err {
			break
		}

		conf := strings.Split(line, ",")
		w.peers = append(w.peers, conf[1])
	}

	start := time.Now()
	//suffix := strconv.Itoa(partitionNum) + "_"

	graphIO, _ := os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "p/G." + strconv.Itoa(w.selfId-1))
	defer graphIO.Close()

	if graphIO == nil {
		fmt.Println("graphIO is nil")
	}

	fxiReader, _ := os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "p/F" + strconv.Itoa(w.selfId-1) + ".I")
	fxoReader, _ := os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "p/F" + strconv.Itoa(w.selfId-1) + ".O")
	defer fxiReader.Close()
	defer fxoReader.Close()

	w.g, err = graph.NewGraphFromTXT(graphIO, fxiReader, fxoReader, strconv.Itoa(w.selfId-1))
	if err != nil {
		log.Fatal(err)
	}

	loadTime := time.Since(start)
	fmt.Printf("loadGraph Time: %v", loadTime)

	if w.g == nil {
		log.Println("can't load graph")
	}
	// Initial some variables from graph
	w.routeTable = algorithm.GenerateRouteTable(w.g.GetFOs())
	w.distance, w.exchangeMsg = Generate(w.g)

	return w
}

func RunWorker(id, partitionNum int) {
	w := newWorker(id, partitionNum)

	log.Println(w.selfId)
	log.Println(w.peers[w.selfId])
	ln, err := net.Listen("tcp", ":"+strings.Split(w.peers[w.selfId], ":")[1])
	if err != nil {
		panic(err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterWorkerServer(grpcServer, w)
	go func() {
		log.Println("start listen")
		if err := grpcServer.Serve(ln); err != nil {
			panic(err)
		}
	}()

	masterHandle, err := grpc.Dial(w.peers[0], grpc.WithInsecure())
	if err != nil {
		log.Fatal(err)
	}
	registerClient := pb.NewMasterClient(masterHandle)
	response, err := registerClient.Register(context.Background(), &pb.RegisterRequest{WorkerIndex: int32(w.selfId)})
	if err != nil || !response.Ok {
		log.Fatal("error for register")
	}

	// wait for stop
	<-w.stopChannel
	log.Println("finish task")
}
