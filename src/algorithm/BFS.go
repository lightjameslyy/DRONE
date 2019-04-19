package algorithm

import (
	"container/heap"
	"graph"
	"time"
	//"fmt"
	"log"
)

// for more information about this implement of priority queue,
// you can reference https://golang.org/pkg/container/heap/
// we use BFSPair for store level message associated with node ID

// NodeId: id of the node in the graph
// Level: BFS level of this node
type BFSPair struct {
	NodeId   int64
	ParentId int64
	Level    int64
}

type BFSQueue []*BFSPair

func (pq BFSQueue) Len() int { return len(pq) }

func (pq BFSQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return pq[i].Level < pq[j].Level
}

func (pq BFSQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *BFSQueue) Push(x interface{}) {
	*pq = append(*pq, x.(*BFSPair))
}

func (pq *BFSQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	p := old[n-1]
	*pq = old[0 : n-1]
	return p
}

// INPUT:
// g: the graph structure of graph
// level: BFS level of nodeId,
// startID: id of search key
// OUTPUT:
// returned bool value indicates whether there is some message to be sent
// the map value is the message need to be send
// map[i] is a list of message need to be sent to partition i
func BFS_PEVal(g graph.Graph, parent map[int64]int64, level map[int64]int64, startID int64,
	updateMaster map[int64]bool, updateMirror map[int64]bool) (bool, map[int][]*BFSPair, float64, float64, int64, int32, int32) {
	log.Printf("start id:%v\n", startID)
	itertationStartTime := time.Now()
	nodes := g.GetNodes()
	// if this partition doesn't include startID, just return
	if _, ok := nodes[startID]; !ok {
		return false, make(map[int][]*BFSPair), 0, 0, 0, 0, 0
	}
	pq := make(BFSQueue, 0)

	startBFSPair := &BFSPair{
		NodeId:   startID,
		ParentId: startID,
		Level:    0,
	}
	heap.Push(&pq, startBFSPair)
	//pq.Push(startBFSPair)
	level[startID] = 0
	parent[startID] = startID

	var iterationNum int64 = 0

	if g.IsMirror(startID) {
		updateMirror[startID] = true
	}

	if g.IsMaster(startID) {
		updateMaster[startID] = true
	}

	for pq.Len() > 0 {
		iterationNum++

		node := heap.Pop(&pq).(*BFSPair)
		curId := node.NodeId
		// pId := node.ParentId
		curLevel := node.Level

		//if curLevel >= level[curId] {
		//	continue
		//}

		for childId, _ := range g.GetTargets(curId) {
			// unvisited or not optimal
			//if parent[childId] == -1 || level[childId] > curLevel+1 {
			if level[childId] > curLevel+1 {
				parent[childId] = curId
				level[childId] = curLevel + 1
				heap.Push(&pq, &BFSPair{
					NodeId:   childId,
					ParentId: curId,
					Level:    curLevel + 1,
				})
				if g.IsMirror(childId) {
					updateMirror[childId] = true
				}
				if g.IsMaster(childId) {
					updateMaster[childId] = true
				}
			}
		}
	}

	combineStartTime := time.Now()
	//end BFS iteration
	messageMap := make(map[int][]*BFSPair)

	mirrors := g.GetMirrors()
	for id := range updateMirror {
		partition := mirrors[id]
		parentId := parent[id]
		lev := level[id]
		if _, ok := messageMap[partition]; !ok {
			messageMap[partition] = make([]*BFSPair, 0)
		}
		messageMap[partition] = append(messageMap[partition],
			&BFSPair{NodeId: id, ParentId: parentId, Level: lev})
	}

	combineTime := time.Since(combineStartTime).Seconds()

	updateBFSPairNum := int32(len(updateMirror))
	dstPartitionNum := int32(len(messageMap))
	iterationTime := time.Since(itertationStartTime).Seconds()
	return len(messageMap) != 0, messageMap, iterationTime, combineTime, iterationNum, updateBFSPairNum, dstPartitionNum
}

// the arguments is similar with PEVal
// the only difference is updated, which is the message this partition received
func BFS_IncEval(g graph.Graph, parent map[int64]int64, level map[int64]int64,
	updated []*BFSPair,
	updateMaster map[int64]bool, updateMirror map[int64]bool, updatedByMessage map[int64]bool, id int) (bool, map[int][]*BFSPair, float64, float64, int64, int32, int32, float64, int32, int32) {
	iterationStartTime := time.Now()
	if len(updated) == 0 && len(updatedByMessage) == 0 {
		return false, make(map[int][]*BFSPair), 0, 0, 0, 0, 0, 0, 0, 0
	}

	pq := make(BFSQueue, 0)

	aggregatorOriSize := int32(len(updated))
	aggregateStart := time.Now()
	aggregateTime := time.Since(aggregateStart).Seconds()
	aggregatorReducedSize := int32(len(updated))

	for _, bfsMsg := range updated {
		if bfsMsg.Level < level[bfsMsg.NodeId] {
			level[bfsMsg.NodeId] = bfsMsg.Level
			parent[bfsMsg.NodeId] = bfsMsg.ParentId
			updatedByMessage[bfsMsg.NodeId] = true
		}
	}

	for id := range updatedByMessage {
		//log.Printf("updatedId: %v\n", id)

		//lev := level[id]
		//pid := parent[id]
		//heap.Push(&pq, startBFSPair)
		heap.Push(&pq, &BFSPair{
			NodeId:   id,
			ParentId: parent[id],
			Level:    level[id],
		})
	}

	log.Printf("worker%v updatedbymessage:%v", id, len(updatedByMessage))

	var iterationNum int64 = 0

	for pq.Len() > 0 {
		iterationNum++

		node := heap.Pop(&pq).(*BFSPair)
		curId := node.NodeId
		// pId := node.ParentId
		curLevel := node.Level

		//if curLevel >= level[curId] {
		//	continue
		//}

		for childId, _ := range g.GetTargets(curId) {
			// unvisited or not optimal
			//if parent[childId] == -1 || level[childId] > curLevel+1 {
			if level[childId] > curLevel+1 {
				parent[childId] = curId
				level[childId] = curLevel + 1
				heap.Push(&pq, &BFSPair{
					NodeId:   childId,
					ParentId: curId,
					Level:    curLevel + 1,
				})
				if g.IsMirror(childId) {
					updateMirror[childId] = true
				}
				if g.IsMaster(childId) {
					updateMaster[childId] = true
				}
			}
		}
	}
	combineStartTime := time.Now()

	messageMap := make(map[int][]*BFSPair)
	mirrors := g.GetMirrors()
	for id := range updateMirror {
		partition := mirrors[id]
		parentId := parent[id]
		lev := level[id]

		//log.Printf("nodeId: %v, Level:%v\n", id, dis)
		if _, ok := messageMap[partition]; !ok {
			messageMap[partition] = make([]*BFSPair, 0)
		}
		messageMap[partition] = append(messageMap[partition],
			&BFSPair{NodeId: id, ParentId: parentId, Level: lev})
	}

	combineTime := time.Since(combineStartTime).Seconds()

	updateBFSPairNum := int32(len(updateMirror))
	dstPartitionNum := int32(len(messageMap))
	iterationTime := time.Since(iterationStartTime).Seconds()
	//log.Printf("zs-log: messageMap:%v\n", messageMap)
	return len(messageMap) != 0 || len(updateMaster) != 0, messageMap, iterationTime, combineTime, iterationNum, updateBFSPairNum, dstPartitionNum, aggregateTime, aggregatorOriSize, aggregatorReducedSize
}
