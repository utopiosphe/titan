package validation

import (
	"time"

	"github.com/Filecoin-Titan/titan/api/types"
)

var (
	firstElectionInterval = 10 * time.Minute   // Time of the first election
	electionCycle         = 1 * 24 * time.Hour // Election cycle
)

func getTimeAfter(t time.Duration) time.Time {
	return time.Now().Add(t)
}

// triggers the election process at a regular interval.
func (m *Manager) startElectionTicker() {
	// validators, err := m.nodeMgr.LoadValidators(m.nodeMgr.ServerID)
	// if err != nil {
	// 	log.Errorf("electionTicker LoadValidators err: %v", err)
	// 	return
	// }

	expiration := m.electionCycle

	// expiration := m.getElectionCycle()
	// if len(validators) <= 0 {
	// 	expiration = firstElectionInterval
	// }

	m.nextElectionTime = getTimeAfter(firstElectionInterval)

	ticker := time.NewTicker(firstElectionInterval)
	defer ticker.Stop()

	doElect := func() {
		m.nextElectionTime = getTimeAfter(expiration)

		ticker.Reset(expiration)
		err := m.elect()
		if err != nil {
			log.Errorf("elect err:%s", err.Error())
		}
	}

	for {
		select {
		case <-ticker.C:
			doElect()
		case <-m.updateCh:
			doElect()
		}
	}
}

// elect triggers an election and updates the list of validators.
func (m *Manager) elect() error {
	log.Debugln("start elect ")

	m.electValidatorsFromEdge()
	// validators, validatables := m.electValidators()

	// m.ResetValidatorGroup(validators, validatables)

	// return m.nodeMgr.UpdateValidators(validators, m.nodeMgr.ServerID)

	return nil
}

func (m *Manager) CompulsoryElection(validators []string, cleanOld bool) error {
	vMap := make(map[string]struct{})

	for _, nid2 := range validators {
		vMap[nid2] = struct{}{}

		node := m.nodeMgr.GetCandidateNode(nid2)
		if node == nil {
			continue
		}

		if node.Type == types.NodeValidator {
			continue
		}

		m.nodeMgr.RepayNodeWeight(node)
		node.Type = types.NodeValidator
	}

	if cleanOld {
		_, nodes := m.nodeMgr.GetAllCandidateNodes()

		for _, node := range nodes {
			if node.Type != types.NodeValidator {
				continue
			}

			if _, exists := vMap[node.NodeID]; !exists {
				node.Type = types.NodeCandidate
				m.nodeMgr.DistributeNodeWeight(node)
			}
		}
	}

	return m.nodeMgr.UpdateValidators(validators, m.nodeMgr.ServerID, cleanOld)
}

// StartElection triggers an election manually.
func (m *Manager) StartElection() {
	// TODO need to add restrictions to disallow frequent calls?
	m.updateCh <- struct{}{}
}

// // performs the election process and returns the list of elected validators.
// func (m *Manager) electValidators() ([]string, []string) {
// 	ratio := m.getValidatorRatio()

// 	list, _ := m.nodeMgr.GetAllCandidateNodes()

// 	needValidatorCount := int(math.Ceil(float64(len(list)) * ratio))
// 	if needValidatorCount <= 0 {
// 		return nil, list
// 	}

// 	rand.Shuffle(len(list), func(i, j int) {
// 		list[i], list[j] = list[j], list[i]
// 	})

// 	if needValidatorCount > len(list) {
// 		needValidatorCount = len(list)
// 	}

// 	validators := list[:needValidatorCount]
// 	validatables := list[needValidatorCount:]

// 	return validators, validatables
// }

func (m *Manager) electValidatorsFromEdge() {
	if m.l2ValidatorCount <= 0 {
		return
	}

	list := m.nodeMgr.GetAllEdgeNode()

	curCount := 0

	for _, node := range list {
		switch node.NATType {
		case types.NatTypeNo:
			if node.IsNewVersion && curCount < m.l2ValidatorCount {
				node.Type = types.NodeValidator
				curCount++
			} else {
				node.Type = types.NodeEdge
			}
		default:
			node.Type = types.NodeEdge
		}
	}
}
