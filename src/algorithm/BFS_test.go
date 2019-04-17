package algorithm

import (
	"container/heap"
	"fmt"
	"testing"
)

func TestBFSQueue_Pop(t *testing.T) {
	pq := make(BFSQueue, 0)
	levels := []int64{3, 4, 5, 1, 2, 2}
	for _, lev := range levels {
		heap.Push(&pq, &BFSPair{
			NodeId:   lev,
			ParentId: lev,
			Level:    lev,
		})
	}
	for pq.Len() > 0 {
		node := heap.Pop(&pq).(*BFSPair)
		fmt.Println(node)
	}

}
