package nat

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Filecoin-Titan/titan/api/types"
	"github.com/Filecoin-Titan/titan/node/scheduler/node"
)

// checks if an edge node is behind a Full Cone NAT
func detectFullConeNAT(ctx context.Context, candidate *node.Node, eURL string) (bool, error) {
	return candidate.API.CheckNetworkConnectable(ctx, "udp", eURL)
}

// checks if an edge node is behind a Restricted NAT
func detectRestrictedNAT(http3Client *http.Client, eURL string) (bool, error) {
	resp, err := http3Client.Get(eURL)
	if err != nil {
		log.Debugf("detectRestrictedNAT failed: %s", err.Error())
		return false, nil
	}
	defer resp.Body.Close()

	return true, nil
}

func isPrivateIP(addr string) bool {
	ipstr, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(ipstr)
	if ip == nil {
		return false
	}

	if ip.To4() != nil {
		ip = ip.To4()

		switch {
		case ip[0] == 10:
			// 10.0.0.0 To 10.255.255.255
			return true
		case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
			// 172.16.0.0 To 172.31.255.255
			return true
		case ip[0] == 192 && ip[1] == 168:
			// 192.168.0.0 To 192.168.255.255
			return true
		}
	}

	return false
}

// determines the NAT type of an edge node
func analyzeNodeNATType(ctx context.Context, bNode *node.Node, candidateNodes []*node.Node, http3Client *http.Client) (types.NatType, error) {
	candidate1 := candidateNodes[0]
	externalAddr, err := bNode.API.ExternalServiceAddress(ctx, candidate1.RPCURL())
	if err != nil {
		return types.NatTypeUnknown, fmt.Errorf("check candidate ExternalServiceAddress %s error %s", bNode.NodeID, err.Error())
	}

	if !isPrivateIP(externalAddr) && externalAddr != bNode.RemoteAddr {
		log.Debugf("check candidate %s to edge %s != %s [%s] [%s]", candidate1.NodeID, externalAddr, bNode.RemoteAddr, bNode.NodeID, candidate1.RPCURL())
		return types.NatTypeSymmetric, nil
	}

	bURL := fmt.Sprintf("https://%s/net", bNode.RemoteAddr)

	candidate2 := candidateNodes[1]
	ok, err := candidate2.API.CheckNetworkConnectable(ctx, "tcp", bURL)
	if err != nil {
		return types.NatTypeUnknown, err
	}

	if ok {
		ok, err = candidate2.API.CheckNetworkConnectable(ctx, "udp", bURL)
		if err != nil {
			return types.NatTypeUnknown, err
		}

		if ok {
			return types.NatTypeNo, nil
		}
	}

	log.Debugf("check candidate %s to edge %s tcp connectivity failed [%s]", candidate2.NodeID, bURL, bNode.NodeID)

	if ok, err := detectFullConeNAT(ctx, candidate2, bURL); err != nil {
		return types.NatTypeUnknown, err
	} else if ok {
		return types.NatTypeFullCone, nil
	}

	log.Debugf("check candidate %s to edge %s udp connectivity failed [%s]", candidate2.NodeID, bURL, bNode.NodeID)

	if isBehindRestrictedNAT, err := detectRestrictedNAT(http3Client, bURL); err != nil {
		return types.NatTypeUnknown, err
	} else if isBehindRestrictedNAT {
		return types.NatTypeRestricted, nil
	}

	return types.NatTypePortRestricted, nil
}

// determineNATType detect the NAT type of an edge node
func determineNodeNATType(bNode *node.Node, candidateNodes []*node.Node, http3Client *http.Client) string {
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	if len(candidateNodes) < miniCandidateCount {
		return bNode.NATType
	}

	natType, err := analyzeNodeNATType(ctx, bNode, candidateNodes, http3Client)
	if err != nil {
		log.Warnf("determineNATType, %s error: %s", bNode.NodeID, err.Error())
		natType = types.NatTypeUnknown
	}
	return natType.String()
}
