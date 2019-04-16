package graph

import (
	"fmt"
	"strings"
	"testing"
)

func TestNode_int64(t *testing.T) {
	node := NewNode(1, 2)
	fmt.Printf("node.int64(): %v\n", node.int64())
	if node.int64() != 1 {
		t.Errorf("expected %v, but got %v", 1, node.int64())
	}
}

func TestNode_String(t *testing.T) {
	node := NewNode(1, 2)
	fmt.Printf("node.String(): %v\n", node.String())
	if node.String() != "1" {
		t.Errorf("expected %v, but got %v", "1", node.String())
	}
}

func TestNode_Attr(t *testing.T) {
	node := NewNode(1, 2)
	fmt.Printf("node.Attr(): %v\n", node.Attr())
	if node.Attr() != 2 {
		t.Errorf("expected %v, but got %v", 2, node.Attr())
	}
}

func TestGraph_GetNode(t *testing.T) {
	g := newGraph()
	n := g.GetNode(1)
	fmt.Println(n)
	if n != nil {
		t.Errorf("expected nil, but got %v", n)
	}
	g.AddNode(NewNode(1, 2))
	n = g.GetNode(1)
	fmt.Println(n)
	if n == nil {
		t.Errorf("expected %v, but got nil", n)
	}
}

func TestGraph_AddNode(t *testing.T) {
	g := newGraph()
	g.AddNode(NewNode(1, 2))
	n := g.GetNode(1)
	fmt.Println(n)
	if n == nil {
		t.Errorf("expected %v, but got nil", n)
	}
}

func TestGraph_AddEdge(t *testing.T) {
	g := newGraph()
	g.AddNode(NewNode(1, 1))
	g.AddNode(NewNode(2, 2))
	g.AddEdge(1, 2, 0.5)
	s := g.GetSources(2)
	if s == nil {
		t.Errorf("expect GetSources not nil, but nil")
	} else {
		fmt.Println(s)
	}
}

func TestNewPatternGraph(t *testing.T) {
	s := "1 1 3 2 3 4\n" +
		"2 2 0\n" +
		"3 3 0\n" +
		"4 4 0\n"
	patFile := strings.NewReader(s)
	if patFile == nil {
		t.Errorf("failed to load pattern file.")
	}
	g, _ := NewPatternGraph(patFile)
	if g == nil {
		t.Errorf("failed to create new pattern graph.")
	}
	count := g.GetNodeCount()
	if count != 4 {
		t.Errorf("expected %v, but got %v", 4, count)
	}
	fmt.Println(g)
}

func TestNewGraphFromTXT(t *testing.T) {
	G := "8 12\n" +
		"12 8\n" +
		"1 12\n" +
		"7 12\n" +
		"7 4\n" +
		"15 12\n" +
		"15 4\n" +
		"15 12\n" +
		"15 4\n" +
		"15 4\n" +
		"15 12\n" +
		"15 12\n" +
		"15 4\n"
	Master := "8 2\n" +
		"15 3 2 1\n"
	Mirror := "12 1\n" +
		"1 2\n" +
		"7 3\n"
	Iso := "0\n" +
		"16\n"
	g, err := NewGraphFromTXT(strings.NewReader(G),
		strings.NewReader(Master),
		strings.NewReader(Mirror),
		strings.NewReader(Iso),
		true,
		true)
	if err != nil {
		t.Errorf("NewGraphFromTXT failed.")
	} else {
		fmt.Println(g.GetNodes())
		fmt.Println(g.GetNodeCount())
		fmt.Println(g.GetMasters())
		fmt.Println(g.GetMirrors())
	}
}
