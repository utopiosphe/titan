package node

const (
	onlineScoreRatio = 100.0

	scoreErr = "Invalid score"
)

func (m *Manager) initLevelScale() {
	m.nodeScoreLevel = map[string][]int{}

	cfg, err := m.config()
	if err != nil {
		log.Errorf("get config err:%s", err.Error())
		return
	}

	m.nodeScoreLevel = cfg.NodeScoreLevel
}

func (m *Manager) getScoreLevel(score int) string {
	for level, rangeScore := range m.nodeScoreLevel {
		if score >= rangeScore[0] && score <= rangeScore[1] {
			return level
		}
	}

	return scoreErr
}

func (m *Manager) getNodeScoreLevel(nodeID string) string {
	// online time
	// info, err := m.LoadNodeInfo(nodeID)
	// if err != nil {
	// 	log.Errorf("LoadNodeInfo err:%s", err.Error())
	// 	return scoreErr
	// }

	// minutes := time.Now().Sub(info.FirstTime).Minutes()
	// onlineRatio := float64(info.OnlineDuration) / minutes
	// if onlineRatio > 1 {
	// 	onlineRatio = 1
	// }

	return m.getScoreLevel(int(onlineScoreRatio * 1))
}
