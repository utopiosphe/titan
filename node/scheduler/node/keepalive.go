package node

import (
	"sync"
	"time"

	"github.com/Filecoin-Titan/titan/api/types"
)

type BatchUpdate struct {
	Nodes      []*types.NodeDynamicInfo
	Details    []*types.ProfitDetails
	OnlineData map[string]int
	SaveDate   time.Time
}

type NodeProcessor interface {
	GetNodes() []*Node
	ProcessSave(*Node, int) (*types.ProfitDetails, int)
}

type EdgeProcessor struct{ *Manager }

func (p *EdgeProcessor) GetNodes() []*Node { return p.GetValidEdgeNode() }
func (p *EdgeProcessor) ProcessSave(node *Node, minute int) (*types.ProfitDetails, int) {
	profitMinute := node.TodayOnlineTimeWindow * 5 / 60
	incr, dInfo := p.GetEdgeBaseProfitDetails(node, profitMinute)
	node.IncomeIncr = incr
	return dInfo, profitMinute
}

type CandidateProcessor struct{ *Manager }

func (p *CandidateProcessor) GetNodes() []*Node { return p.GetAllCandidateNodes() }
func (p *CandidateProcessor) ProcessSave(node *Node, minute int) (*types.ProfitDetails, int) {
	var dInfo *types.ProfitDetails
	profitMinute := node.TodayOnlineTimeWindow * 5 / 60
	if !node.IsAbnormal() && qualifiedNAT(node.NATType) {
		dInfo = p.GetCandidateBaseProfitDetails(node, profitMinute)
	}
	return dInfo, profitMinute
}

type L3Processor struct{ *Manager }

func (p *L3Processor) GetNodes() []*Node { return p.GetValidL3Node() }
func (p *L3Processor) ProcessSave(node *Node, minute int) (*types.ProfitDetails, int) {
	profitMinute := node.TodayOnlineTimeWindow * 5 / 60
	incr, dInfo := p.GetEdgeBaseProfitDetails(node, profitMinute)
	node.IncomeIncr = incr
	return dInfo, profitMinute
}

type L5Processor struct{ *Manager }

func (p *L5Processor) GetNodes() []*Node { return p.GetValidL5Node() }
func (p *L5Processor) ProcessSave(node *Node, minute int) (*types.ProfitDetails, int) {
	profitMinute := node.TodayOnlineTimeWindow * 5 / 60
	return nil, profitMinute
}

func (m *Manager) startNodeKeepaliveTimer() {
	<-time.After(10 * time.Minute)
	m.nodesKeepalive(10, true)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	saveCounter := 0
	for range ticker.C {

		saveCounter++
		if saveCounter == 2 {
			m.nodesKeepalive(1, true)
			saveCounter = 0
		} else {
			m.nodesKeepalive(1, false)
		}
	}
}

func (m *Manager) processNodes(processor NodeProcessor, t time.Time, minute int, isSave bool) ([]*types.NodeDynamicInfo, []*types.ProfitDetails, map[string]int) {
	var nodes []*types.NodeDynamicInfo
	var detailsList []*types.ProfitDetails
	nodeOnlineCount := make(map[string]int)

	for _, node := range processor.GetNodes() {
		if m.checkNodeStatus(node, t) {
			node.OnlineDuration += minute
			node.TodayOnlineTimeWindow += (minute * 60) / 5
		}

		if isSave {
			if dInfo, _ := processor.ProcessSave(node, minute); dInfo != nil {
				detailsList = append(detailsList, dInfo)
			}
			nodes = append(nodes, &node.NodeDynamicInfo)
			nodeOnlineCount[node.NodeID] = node.TodayOnlineTimeWindow
			node.TodayOnlineTimeWindow = 0
		}
	}

	return nodes, detailsList, nodeOnlineCount
}

// // startNodeKeepaliveTimer periodically sends keepalive requests to all nodes and checks if any nodes have been offline for too long
// func (m *Manager) startNodeKeepaliveTimer() {
// 	time.Sleep(penaltyFreeTime)

// 	ticker := time.NewTicker(keepaliveTime)
// 	defer ticker.Stop()

// 	minute := 10 // penalty free time
// 	count := 2

// 	for {
// 		<-ticker.C

// 		m.nodesKeepalive(minute, count == 2)
// 		minute = 1
// 		if count == 2 {
// 			count = 0
// 		}
// 		count++
// 	}
// }

func (m *Manager) nodesKeepalive(minute int, isSave bool) {
	now := time.Now()
	t := now.Add(-keepaliveTime)
	timeWindow := (minute * 60) / 5
	m.serverTodayOnlineTimeWindow += timeWindow

	processors := []NodeProcessor{
		&EdgeProcessor{m},
		&CandidateProcessor{m},
		&L3Processor{m},
		&L5Processor{m},
	}

	batch := BatchUpdate{
		SaveDate:   time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()),
		OnlineData: make(map[string]int),
		Nodes:      make([]*types.NodeDynamicInfo, 0),
		Details:    make([]*types.ProfitDetails, 0),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, p := range processors {
		wg.Add(1)
		go func(p NodeProcessor) {
			defer wg.Done()
			nodes, details, counts := m.processNodes(p, t, minute, isSave)

			mu.Lock()
			defer mu.Unlock()
			batch.Nodes = append(batch.Nodes, nodes...)
			batch.Details = append(batch.Details, details...)
			for k, v := range counts {
				batch.OnlineData[k] = v
			}
		}(p)
	}

	wg.Wait()

	if isSave {
		batch.OnlineData[string(m.ServerID)] = m.serverTodayOnlineTimeWindow
		m.serverTodayOnlineTimeWindow = 0

		err := m.UpdateNodeDynamicInfo(batch.Nodes)
		if err != nil {
			log.Errorf("updateNodeData UpdateNodeDynamicInfo err:%s", err.Error())
		}

		err = m.AddNodeProfitDetails(batch.Details)
		if err != nil {
			log.Errorf("updateNodeData AddNodeProfits err:%s", err.Error())
		}

		err = m.UpdateNodeOnlineCount(batch.OnlineData, batch.SaveDate)
		if err != nil {
			log.Errorf("updateNodeData UpdateNodeOnlineCount err:%s", err.Error())
		}
	}
}

// // nodesKeepalive checks all nodes in the manager's lists for keepalive
// func (m *Manager) nodesKeepalive(minute int, isSave bool) {
// 	now := time.Now()
// 	t := now.Add(-keepaliveTime)
// 	timeWindow := (minute * 60) / 5

// 	m.serverTodayOnlineTimeWindow += timeWindow

// 	nodes := make([]*types.NodeDynamicInfo, 0)
// 	detailsList := make([]*types.ProfitDetails, 0)
// 	nodeOnlineCount := make(map[string]int, 0)

// 	saveDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

// 	eList := m.GetValidEdgeNode()
// 	for _, node := range eList {
// 		if m.checkNodeStatus(node, t) {
// 			node.OnlineDuration += minute
// 			node.TodayOnlineTimeWindow += timeWindow
// 		}

// 		if isSave {
// 			profitMinute := node.TodayOnlineTimeWindow * 5 / 60

// 			incr, dInfo := m.GetEdgeBaseProfitDetails(node, profitMinute)
// 			if dInfo != nil {
// 				detailsList = append(detailsList, dInfo)
// 			}
// 			node.IncomeIncr = incr
// 			nodes = append(nodes, &node.NodeDynamicInfo)

// 			nodeOnlineCount[node.NodeID] = node.TodayOnlineTimeWindow
// 			node.TodayOnlineTimeWindow = 0
// 		}
// 	}

// 	cList := m.GetAllCandidateNodes()
// 	for _, node := range cList {
// 		if m.checkNodeStatus(node, t) {
// 			if !node.IsAbnormal() {
// 				node.OnlineDuration += minute
// 				node.TodayOnlineTimeWindow += timeWindow
// 			}
// 		}

// 		if isSave {
// 			if !node.IsAbnormal() && qualifiedNAT(node.NATType) {
// 				profitMinute := node.TodayOnlineTimeWindow * 5 / 60

// 				dInfo := m.GetCandidateBaseProfitDetails(node, profitMinute)
// 				if dInfo != nil {
// 					detailsList = append(detailsList, dInfo)
// 				}
// 			}
// 			nodes = append(nodes, &node.NodeDynamicInfo)

// 			nodeOnlineCount[node.NodeID] = node.TodayOnlineTimeWindow
// 			node.TodayOnlineTimeWindow = 0
// 		}
// 	}

// 	l3List := m.GetValidL3Node()
// 	for _, node := range l3List {
// 		if m.checkNodeStatus(node, t) {
// 			node.OnlineDuration += minute
// 			node.TodayOnlineTimeWindow += timeWindow
// 		}

// 		if isSave {
// 			profitMinute := node.TodayOnlineTimeWindow * 5 / 60

// 			incr, dInfo := m.GetEdgeBaseProfitDetails(node, profitMinute)
// 			if dInfo != nil {
// 				detailsList = append(detailsList, dInfo)
// 			}
// 			node.IncomeIncr = incr
// 			nodes = append(nodes, &node.NodeDynamicInfo)

// 			nodeOnlineCount[node.NodeID] = node.TodayOnlineTimeWindow
// 			node.TodayOnlineTimeWindow = 0
// 		}
// 	}

// 	l5List := m.GetValidL5Node()
// 	for _, node := range l5List {
// 		if m.checkNodeStatus(node, t) {
// 			node.OnlineDuration += minute
// 			node.TodayOnlineTimeWindow += timeWindow
// 		}

// 		if isSave {
// 			nodeOnlineCount[node.NodeID] = node.TodayOnlineTimeWindow
// 			node.TodayOnlineTimeWindow = 0
// 		}
// 	}

// 	if !isSave {
// 		return
// 	}
// 	log.Infoln("nodesKeepalive save info start")

// 	nodeOnlineCount[string(m.ServerID)] = m.serverTodayOnlineTimeWindow
// 	m.serverTodayOnlineTimeWindow = 0

// 	err := m.UpdateNodeDynamicInfo(nodes)
// 	if err != nil {
// 		log.Errorf("updateNodeData UpdateNodeDynamicInfo err:%s", err.Error())
// 	}

// 	err = m.AddNodeProfitDetails(detailsList)
// 	if err != nil {
// 		log.Errorf("updateNodeData AddNodeProfits err:%s", err.Error())
// 	}

// 	err = m.UpdateNodeOnlineCount(nodeOnlineCount, saveDate)
// 	if err != nil {
// 		log.Errorf("updateNodeData UpdateNodeOnlineCount err:%s", err.Error())
// 	}

// 	log.Infoln("nodesKeepalive save info done")
// }

// SetNodeOffline removes the node's IP and geo information from the manager.
func (m *Manager) SetNodeOffline(node *Node) {
	m.IPMgr.RemoveNodeIP(node.NodeID, node.ExternalIP)
	m.GeoMgr.RemoveNodeGeo(node.NodeID, node.Type, node.AreaID)

	if node.Type == types.NodeCandidate {
		m.deleteCandidateNode(node)
	} else if node.Type == types.NodeEdge {
		m.deleteEdgeNode(node)
	} else if node.Type == types.NodeL5 {
		m.deleteL5Node(node)
	} else if node.Type == types.NodeL3 {
		m.deleteL3Node(node)
	}

	log.Infof("node offline %s, %s", node.NodeID, node.ExternalIP)
}

// checkNodeStatus checks if a node has sent a keepalive recently and updates node status accordingly
func (m *Manager) checkNodeStatus(node *Node, t time.Time) bool {
	lastTime := node.LastRequestTime()

	if !lastTime.After(t) {
		m.SetNodeOffline(node)

		return false
	}

	return true
}
