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

// forward the port defined in ssh and map to local port localhost:8822
-tcplisten ssh:8822

// forward the port defined in sshelsewhere and map to local port 0.0.0.0:8823
-tcplisten sshelsewhere:0.0.0.0:8823

// forward the port defined in sshelsewhere and map to local port 192.168.0.48:8823
-tcplisten sshelsewhere:192.168.0.48:8823

// forward the port 10010 from the range of 10000-10010 and map to local port localhost:8824
-tcplisten range-10010:8824
*/

type ForwardTargetType int

const (
	ForwardTargetTypeTCP ForwardTargetType = iota
	ForwardTargetTypeListener
)

// ForwardTargetPort represents a target port to forward from the server to the client, or a target listener from the server to the client
type ForwardTargetPort struct {
	TargetName        string
	Host              string
	StartPort         int
	PortCount         int
	ForwardTargetType ForwardTargetType
}

// represents a client-side port forward to a target port
type ListenTargetPort struct {
	TargetName string
	LocalHost  string
	LocalPort  int
	PortIndex  int
}

// ParseListenTargetPortsFromStringSlice parses a slice of forward target ports from a string slice
func ParseListenTargetPortsFromStringSlice(ids []string) (map[string]*ListenTargetPort, error) {
	listenPorts := make(map[string]*ListenTargetPort)
	for _, id := range ids {
		listenPort, err := ParseListenTargetPortFromString(id)
		if err != nil {
			return nil, err
		}
		listenPorts[listenPort.TargetName] = listenPort
	}
	return listenPorts, nil
}

// ParseListenTargetPortFromString parses a listen target port from a string
// the string should be in the format of 'targetname=host:port' where targetname
// can be a server-side forward target name followed by an optional port index separated by a dash
func ParseListenTargetPortFromString(id string) (*ListenTargetPort, error) {
	// split the id into the target name and the host/port
	idparts := strings.Split(id, "=")
	if len(idparts) != 2 {
		return nil, errors.New("invalid forward ID")
	}

	// split the host/port into the host and port
	localHostParts := strings.Split(idparts[1], ":")
	if len(localHostParts) != 2 {
		return nil, errors.New("invalid host/port")
	}

	// split the target name into the target name and the port index
	targetParts := strings.Split(idparts[0], "-")
	portIndex := 0
	var err error
	if len(targetParts) == 2 {
		portIndex, err = strconv.Atoi(targetParts[0])
		if err != nil {
			return nil, errors.New("invalid target port index")
		}
	} else if len(targetParts) > 2 {
		return nil, errors.New("invalid target name")
	}

	localHostPort, err := strconv.Atoi(localHostParts[1])
	if err != nil {
		return nil, errors.New("invalid local port")
	}

	return &ListenTargetPort{
		TargetName: targetParts[0],
		LocalHost:  localHostParts[0],
		LocalPort:  localHostPort,
		PortIndex:  portIndex,
	}, nil
}

// ParseForwardTargetPortFromString parses a forward target port from a string
// the string should be in the format of 'targetname=host:port-range'
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
		ForwardTargetType: ForwardTargetTypeTCP,
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
