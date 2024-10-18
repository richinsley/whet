package pkg

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

/*
// Forward IDs MUST be alphanumeric and MAY contain underscores but MUST not contain spaces or hyphens.
// Forward IDs are not case-sensitive.

// forward local SSH port with the forward id of 'ssh'
-tcptarget ssh=localhost:22

// forward a non-local ssh port with the forward id of 'sshelsewhere'
-tcptarget sshelsewhere=192.168.0.33:22

// create a range of local ports with the forward id of 'localrange'
-tcptarget range=localhost:10000-10010

// create a range of non-local ports with the forward id of 'remoterange'
-tcptarget remoterange=192.168.0.33:10000-10010
*/

type ForwardTargetPort struct {
	TargetName string
	Host       string
	StartPort  int
	PortCount  int
}

func ParseForwardTargetPortFromString(id string) (*ForwardTargetPort, error) {
	// split the id into the target name and the host/port
	parts := strings.Split(id, "=")
	if len(parts) != 2 {
		return nil, errors.New("invalid forward ID")
	}

	// split the host/port into the host and port range
	hostParts := strings.Split(parts[1], ":")
	if len(hostParts) != 2 {
		return nil, errors.New("invalid host/port")
	}

	// split the port range into start and end ports
	portParts := strings.Split(hostParts[1], "-")
	if len(portParts) == 1 {
		portParts = append(portParts, portParts[0])
	}

	// convert the port range to integers
	startPort, err := strconv.Atoi(portParts[0])
	if err != nil {
		return nil, err
	}
	endPort, err := strconv.Atoi(portParts[1])
	if err != nil {
		return nil, err
	}

	// ensure start port is less than or equal to end port
	if startPort > endPort {
		return nil, fmt.Errorf("invalid port rangen %d-%d", startPort, endPort)
	}

	return &ForwardTargetPort{
		TargetName: parts[0],
		Host:       hostParts[0],
		StartPort:  startPort,
		PortCount:  endPort - startPort + 1,
	}, nil
}

func ParseForwardTargetPortsFromStringSlice(ids []string) (map[string]*ForwardTargetPort, error) {
	forwardPorts := make(map[string]*ForwardTargetPort)
	for _, id := range ids {
		forwardPort, err := ParseForwardTargetPortFromString(id)
		if err != nil {
			return nil, err
		}
		forwardPorts[forwardPort.TargetName] = forwardPort
	}
	return forwardPorts, nil
}
