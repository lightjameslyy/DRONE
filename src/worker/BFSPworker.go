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
	"net"
	"os"
	pb "protobuf"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"tools"
)

func GenerateLevel(g graph.Graph) map[int64]int64 {
	level := make(map[int64]int64)

	for id := range g.GetNodes() {
		level[id] = 1<<63 - 1
	}
	return level
}

func GenerateParent(g graph.Graph) map[int64]int64 {
	parent := make(map[int64]int64)

	for id := range g.GetNodes() {
		parent[id] = -1
	}
	return parent
}

// rpc send has max size limit, so we spilt our transfer into many small block
func Peer2PeerBFSSend(client pb.WorkerClient, message []*pb.BFSMessageStruct, id int, calculateStep bool) {
	for len(message) > tools.RPCSendSize {
		slice := message[0:tools.RPCSendSize]
		message = message[tools.RPCSendSize:]
		_, err := client.BFSSend(context.Background(), &pb.BFSMessageRequest{Pair: slice, CalculateStep: calculateStep})
		if err != nil {
			log.Printf("send to %v error\n", id)
			log.Fatal(err)
		}
	}
	if len(message) != 0 {
		_, err := client.BFSSend(context.Background(), &pb.BFSMessageRequest{Pair: message, CalculateStep: calculateStep})
		if err != nil {
			log.Printf("send to %v error\n", id)
			log.Fatal(err)
		}
	}
}

type BFSWorker struct {
	mutex *sync.Mutex

	peers        []string
	selfId       int // the id of this worker itself in workers
	grpcHandlers map[int]*grpc.ClientConn
	workerNum    int

	g      graph.Graph
	parent map[int64]int64
	level  map[int64]int64 //
	//exchangeMsg map[graph.ID]float64
	updatedBuffer    []*algorithm.BFSPair
	exchangeBuffer   []*algorithm.BFSPair
	updatedMaster    map[int64]bool
	updatedMirror    map[int64]bool
	updatedByMessage map[int64]bool

	iterationNum int
	stopChannel  chan bool

	calTime  float64
	sendTime float64
}

func (w *BFSWorker) Lock() {
	w.mutex.Lock()
}

func (w *BFSWorker) UnLock() {
	w.mutex.Unlock()
}

func (w *BFSWorker) ShutDown(ctx context.Context, args *pb.ShutDownRequest) (*pb.ShutDownResponse, error) {
	log.Println("receive shutDown request")
	log.Printf("worker %v calTime:%v sendTime:%v", w.selfId, w.calTime, w.sendTime)
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

func (w *BFSWorker) BFSMessageSend(messages map[int][]*algorithm.BFSPair, calculateStep bool) []*pb.WorkerCommunicationSize {
	SlicePeerSend := make([]*pb.WorkerCommunicationSize, 0)
	var wg sync.WaitGroup
	messageLen := len(messages)
	batch := (messageLen + tools.ConnPoolSize - 1) / tools.ConnPoolSize

	indexBuffer := make([]int, 0)
	for partitionId := range messages {
		indexBuffer = append(indexBuffer, partitionId)
	}
	sort.Ints(indexBuffer)
	start := 0
	for i := 1; i < len(indexBuffer); i++ {
		if indexBuffer[i] > w.selfId {
			start = i
			break
		}
	}
	indexBuffer = append(indexBuffer[start:], indexBuffer[:start]...)

	for i := 1; i <= batch; i++ {
		for j := (i - 1) * tools.ConnPoolSize; j < i*tools.ConnPoolSize && j < len(indexBuffer); j++ {
			partitionId := indexBuffer[j]
			message := messages[partitionId]
			wg.Add(1)

			eachWorkerCommunicationSize := &pb.WorkerCommunicationSize{WorkerID: int32(partitionId + 1), CommunicationSize: int32(len(message))}
			SlicePeerSend = append(SlicePeerSend, eachWorkerCommunicationSize)

			go func(partitionId int, message []*algorithm.BFSPair) {
				defer wg.Done()
				workerHandle, err := grpc.Dial(w.peers[partitionId+1], grpc.WithInsecure())
				if err != nil {
					log.Fatal(err)
				}
				defer workerHandle.Close()

				client := pb.NewWorkerClient(workerHandle)
				encodeMessage := make([]*pb.BFSMessageStruct, 0)
				for _, msg := range message {
					encodeMessage = append(encodeMessage,
						&pb.BFSMessageStruct{NodeID: msg.NodeId,
							ParentId: msg.ParentId,
							Level:    msg.Level})
				}
				Peer2PeerBFSSend(client, encodeMessage, partitionId+1, calculateStep)
			}(partitionId, message)

		}
		wg.Wait()
	}
	return SlicePeerSend
}

func (w *BFSWorker) peval(args *pb.PEvalRequest, id int) {
	var fullSendStart time.Time
	var fullSendDuration float64
	var SlicePeerSend []*pb.WorkerCommunicationSize
	calculateStart := time.Now()

	startId := int64(1)

	isMessageToSend, messages, _, combineTime, iterationNum, updatePairNum,
		dstPartitionNum := algorithm.BFS_PEVal(w.g, w.parent, w.level, startId,
		w.updatedMaster, w.updatedMirror)

	//log.Printf("lt in peval worker %v: parent: %v\n", w.selfId, w.parent)
	//log.Printf("lt in peval worker %v: level: %v\n", w.selfId, w.level)
	//log.Printf("lt in peval worker %v: updatedMaster: %v\n", w.selfId, w.updatedMaster)
	//log.Printf("lt in peval worker %v: updatedMirror: %v\n", w.selfId, w.updatedMirror)

	//log.Printf("zs-log:worker%v visited:%v, percent:%v%%\n", id, w.visited.Size(), float64(w.visited.Size()) / float64(len(w.g.GetNodes())))
	calculateTime := time.Since(calculateStart).Seconds()

	if !isMessageToSend {
		var SlicePeerSendNull []*pb.WorkerCommunicationSize // this struct only for hold place. contains nothing, client end should ignore it

		masterHandle := w.grpcHandlers[0]
		Client := pb.NewMasterClient(masterHandle)

		finishRequest := &pb.FinishRequest{AggregatorOriSize: 0,
			AggregatorSeconds: 0, AggregatorReducedSize: 0, IterationSeconds: calculateTime,
			CombineSeconds: combineTime, IterationNum: iterationNum, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: 0,
			PairNum: SlicePeerSendNull, WorkerID: int32(id), MessageToSend: isMessageToSend}

		Client.SuperStepFinish(context.Background(), finishRequest)
		return
	} else {
		fullSendStart = time.Now()
		SlicePeerSend = w.BFSMessageSend(messages, true)
	}

	fullSendDuration = time.Since(fullSendStart).Seconds()

	masterHandle := w.grpcHandlers[0]
	Client := pb.NewMasterClient(masterHandle)
	w.calTime += calculateTime
	w.sendTime += fullSendDuration
	finishRequest := &pb.FinishRequest{AggregatorOriSize: 0,
		AggregatorSeconds: 0, AggregatorReducedSize: 0, IterationSeconds: calculateTime,
		CombineSeconds: combineTime, IterationNum: iterationNum, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: fullSendDuration,
		PairNum: SlicePeerSend, WorkerID: int32(id), MessageToSend: isMessageToSend}

	Client.SuperStepFinish(context.Background(), finishRequest)
}

func (w *BFSWorker) PEval(ctx context.Context, args *pb.PEvalRequest) (*pb.PEvalResponse, error) {
	go w.peval(args, w.selfId)
	return &pb.PEvalResponse{Ok: true}, nil
}

func (w *BFSWorker) incEval(args *pb.IncEvalRequest, id int) {
	calculateStart := time.Now()
	w.iterationNum++

	isMessageToSend, messages, _, combineTime, iterationNum, updatePairNum, dstPartitionNum, aggregateTime,
		aggregatorOriSize, aggregatorReducedSize := algorithm.BFS_IncEval(w.g, w.parent,
		w.level, w.exchangeBuffer, w.updatedMaster, w.updatedMirror, w.updatedByMessage, id)

	//log.Printf("zs-log: worker:%v visited:%v, percent:%v%%\n", id, w.visited.Size(), float64(w.visited.Size()) / float64(len(w.g.GetNodes())))

	w.exchangeBuffer = make([]*algorithm.BFSPair, 0)
	w.updatedMirror = make(map[int64]bool)
	w.updatedByMessage = make(map[int64]bool)

	var fullSendStart time.Time
	var fullSendDuration float64
	SlicePeerSend := make([]*pb.WorkerCommunicationSize, 0)

	calculateTime := time.Since(calculateStart).Seconds()

	if !isMessageToSend {
		var SlicePeerSendNull []*pb.WorkerCommunicationSize // this struct only for hold place, contains nothing

		masterHandle := w.grpcHandlers[0]
		Client := pb.NewMasterClient(masterHandle)

		finishRequest := &pb.FinishRequest{AggregatorOriSize: aggregatorOriSize,
			AggregatorSeconds: aggregateTime, AggregatorReducedSize: aggregatorReducedSize, IterationSeconds: calculateTime,
			CombineSeconds: combineTime, IterationNum: iterationNum, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: 0,
			PairNum: SlicePeerSendNull, WorkerID: int32(id), MessageToSend: isMessageToSend}

		Client.SuperStepFinish(context.Background(), finishRequest)
		return
	} else {
		fullSendStart = time.Now()
		SlicePeerSend = w.BFSMessageSend(messages, true)
	}
	fullSendDuration = time.Since(fullSendStart).Seconds()

	masterHandle := w.grpcHandlers[0]
	Client := pb.NewMasterClient(masterHandle)

	finishRequest := &pb.FinishRequest{AggregatorOriSize: aggregatorOriSize,
		AggregatorSeconds: aggregateTime, AggregatorReducedSize: aggregatorReducedSize, IterationSeconds: calculateTime,
		CombineSeconds: combineTime, IterationNum: iterationNum, UpdatePairNum: updatePairNum, DstPartitionNum: dstPartitionNum, AllPeerSend: fullSendDuration,
		PairNum: SlicePeerSend, WorkerID: int32(id), MessageToSend: isMessageToSend}
	w.calTime += calculateTime
	w.sendTime += fullSendDuration
	Client.SuperStepFinish(context.Background(), finishRequest)
}

func (w *BFSWorker) IncEval(ctx context.Context, args *pb.IncEvalRequest) (*pb.IncEvalResponse, error) {
	go w.incEval(args, w.selfId)
	return &pb.IncEvalResponse{Update: true}, nil
}

func (w *BFSWorker) Assemble(ctx context.Context, args *pb.AssembleRequest) (*pb.AssembleResponse, error) {
	var f *os.File
	if tools.WorkerOnSC {
		f, _ = os.Create(tools.ResultPath + "bfsresult_" + strconv.Itoa(w.
			selfId-1))
	} else {
		f, _ = os.Create(tools.ResultPath + "/result_" + strconv.Itoa(w.selfId-1))
	}
	writer := bufio.NewWriter(f)
	defer f.Close()

	writer.WriteString("NodeId\tParentId\tLevel\n")
	for id, lev := range w.level {
		if !w.g.IsMirror(id) && lev != 1<<63-1 {
			writer.WriteString(
				strconv.FormatInt(id, 10) + "\t" +
					strconv.FormatInt(w.parent[id], 10) + "\t" +
					strconv.FormatInt(lev, 10) + "\n")
		}
	}
	writer.Flush()

	return &pb.AssembleResponse{Ok: true}, nil
}

func (w *BFSWorker) ExchangeMessage(ctx context.Context, args *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	calculateStart := time.Now()
	for _, pair := range w.updatedBuffer {
		id := pair.NodeId
		parentId := pair.ParentId
		lev := pair.Level

		if lev == w.level[id] {
			continue
		}

		if lev < w.level[id] {
			w.level[id] = lev
			w.parent[id] = parentId
			w.updatedByMessage[id] = true
		}
		w.updatedMaster[id] = true
	}
	w.updatedBuffer = make([]*algorithm.BFSPair, 0)

	master := w.g.GetMasters()
	messageMap := make(map[int][]*algorithm.BFSPair)
	for id := range w.updatedMaster {
		for _, partition := range master[id] {
			if _, ok := messageMap[partition]; !ok {
				messageMap[partition] = make([]*algorithm.BFSPair, 0)
			}
			messageMap[partition] = append(messageMap[partition],
				&algorithm.BFSPair{NodeId: id, ParentId: w.parent[id], Level: w.
					level[id]})
		}
	}

	calculateTime := time.Since(calculateStart).Seconds()
	messageStart := time.Now()

	w.BFSMessageSend(messageMap, false)
	messageTime := time.Since(messageStart).Seconds()

	w.updatedMaster = make(map[int64]bool)

	w.calTime += calculateTime
	w.sendTime += messageTime
	return &pb.ExchangeResponse{Ok: true}, nil
}

func (w *BFSWorker) BFSSend(ctx context.Context, args *pb.BFSMessageRequest) (*pb.BFSMessageResponse, error) {
	decodeMessage := make([]*algorithm.BFSPair, 0)

	for _, msg := range args.Pair {
		decodeMessage = append(decodeMessage, &algorithm.BFSPair{NodeId: msg.
			NodeID, ParentId: msg.ParentId, Level: msg.Level})
	}
	w.Lock()
	if args.CalculateStep {
		w.updatedBuffer = append(w.updatedBuffer, decodeMessage...)
	} else {
		w.exchangeBuffer = append(w.exchangeBuffer, decodeMessage...)
	}
	w.UnLock()

	return &pb.BFSMessageResponse{}, nil
}

func (w *BFSWorker) SSSPSend(ctx context.Context, args *pb.SSSPMessageRequest) (
	*pb.SSSPMessageResponse, error) {
	return nil, nil
}

func (w *BFSWorker) SimSend(ctx context.Context, args *pb.SimMessageRequest) (*pb.SimMessageResponse, error) {
	return nil, nil
}
func (w *BFSWorker) PRSend(ctx context.Context, args *pb.PRMessageRequest) (*pb.PRMessageResponse, error) {
	return nil, nil
}

func newBFSWorker(id, partitionNum int) *BFSWorker {
	w := new(BFSWorker)
	w.mutex = new(sync.Mutex)
	w.selfId = id
	w.peers = make([]string, 0)
	w.updatedBuffer = make([]*algorithm.BFSPair, 0)
	w.exchangeBuffer = make([]*algorithm.BFSPair, 0)
	w.updatedMaster = make(map[int64]bool)
	w.updatedMirror = make(map[int64]bool)
	w.updatedByMessage = make(map[int64]bool)
	w.iterationNum = 0
	w.stopChannel = make(chan bool)
	w.grpcHandlers = make(map[int]*grpc.ClientConn)

	w.calTime = 0.0
	w.sendTime = 0.0

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

	w.workerNum = partitionNum
	start := time.Now()

	var graphIO, master, mirror, isolated *os.File

	if tools.WorkerOnSC {
		graphIO, _ = os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "/G." + strconv.Itoa(w.selfId-1))
	} else {
		graphIO, _ = os.Open(tools.NFSPath + "/G." + strconv.Itoa(w.selfId-1))
	}
	defer graphIO.Close()

	if graphIO == nil {
		fmt.Println("graph is nil")
	}
	if tools.WorkerOnSC {
		master, _ = os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "/Master." + strconv.Itoa(w.selfId-1))
		mirror, _ = os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "/Mirror." + strconv.Itoa(w.selfId-1))
		isolated, _ = os.Open(tools.NFSPath + strconv.Itoa(partitionNum) + "/Isolateds." + strconv.Itoa(w.selfId-1))
	} else {
		master, _ = os.Open(tools.NFSPath + "/Master." + strconv.Itoa(w.selfId-1))
		mirror, _ = os.Open(tools.NFSPath + "/Mirror." + strconv.Itoa(w.selfId-1))
		isolated, _ = os.Open(tools.NFSPath + "/Isolateds." + strconv.Itoa(w.selfId-1))
	}
	defer master.Close()
	defer mirror.Close()
	defer isolated.Close()

	w.g, err = graph.NewGraphFromTXT(graphIO, master, mirror, isolated, true, false)
	if err != nil {
		log.Fatal(err)
	}

	loadTime := time.Since(start)
	fmt.Printf("loadGraph Time: %v", loadTime)
	log.Printf("graph size:%v\n", len(w.g.GetNodes()))

	//log.Printf("lt partition %v: nodes: %v\n", id, w.g.GetNodes())
	//log.Printf("lt partition %v: node count: %v\n", id, w.g.GetNodeCount())
	//log.Printf("lt partition %v: masters: %v\n", id, w.g.GetMasters())
	//log.Printf("lt partition %v: mirrors: %v\n", id, w.g.GetMirrors())

	if w.g == nil {
		log.Println("can't load graph")
	}
	w.level = GenerateLevel(w.g)
	w.parent = GenerateParent(w.g)

	return w
}

func RunBFSWorker(id, partitionNum int) {
	w := newBFSWorker(id, partitionNum)

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
	w.grpcHandlers[0] = masterHandle
	defer masterHandle.Close()
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
