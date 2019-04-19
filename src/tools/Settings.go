package tools

const (
	//ResultPath = "/home/acgict/result/"
	//ResultPath = "/data/xwen/"
	ResultPath = "/mnt/nfs/liutao/DRONE_ON_SC/result/"
	//ResultPath = "./"
	//NFSPath = "/home/xwen/graph/16/"
	//NFSPath = "/data/xwen/webbase"
	// NFSPath = "/data/xwen/data"
	NFSPath = "/mnt/nfs/liutao/generate_graph"
	//NFSPath = "../test_data/subgraph.json"
	//PartitionPath = "../test_data/partition.json"
	//NFSPath = "/home/acgict/inputgraph/"

	// WorkerOnSC = true
	WorkerOnSC = false

	//ConfigPath = "../test_data/config.txt"
	ConfigPath = "config.txt"
	//PatternPath = "../test_data/pattern.txt"
	PatternPath              = "pattern.txt"
	GraphSimulationTypeModel = 100

	RPCSendSize = 100000

	ConnPoolSize       = 16
	MasterConnPoolSize = 1024
)
