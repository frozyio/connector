package app

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

type dataFwdRemotePart struct {
	writer io.Writer
	reader io.Reader
	closer io.Closer
	// Channel to signalling that forwarder stops their work on current connection part
	// NOTE: this channel must listen paired connection parent to have ability to stop their work
	forwardStopChannel chan time.Time
}

// dataForwarder implements all job for data transfer between local TCP connection
// and SSH data channel.
// For session transfer from one SSH channel to another this subsystem does some
// interface to control this process from upper level paired connection subsystem
type dataForwarder struct {
	// data forwarder IDX. Used for accounting of instances
	dataForwarderIdx uint32
	// logger for usage in data forwarder
	logger *log.Entry
	// type of local connection
	intentConnection bool
	// interface to local TCP connection
	localTcpConn net.Conn
	// interface to remote connection (SSH channel)
	// NOTE: forwarder can process up to 2 remote data interfaces. Until first is working, second
	// interface is not processed
	remConn     [2]*dataFwdRemotePart
	remConnLock sync.Mutex
	// atomic variable that signals that forwarder must stop to send data to remote part of connection
	// NOTE: After closing (from external side) primary channel, forwarder will replace new registered
	// channel with primary
	dataForwardStopped atomicStatusData
	// flag that signalled that RX forwarder transaction is active
	dataForwardInUpstreamTransaction atomicStatusData
	// flag that signalled that TX forwarder transaction is active
	dataForwardInDownstreamTransaction atomicStatusData
}

type DataForwarderIf interface {
	// Registers another remote part for data forwarding
	// NOTE: Data forwarder can register only one additional remote part
	// so until main remote part is working and one aditional remote part already registered no more
	// remote connection parts can be registered
	RegisterNewRemotePart(newPartChannel *dataFwdRemotePart) error
	// StopForward stops forwarding data from local connection to remote side
	// Received data from remote side continues send to local connection
	StopForward()
	// StartForward starts data forward to remote side
	StartForward()
	// Gets Data Forwarder`s forwarding states of Upstream/Downstream halfs
	GetForwardStates() (upState bool, downState bool)
}

var dataForwarderIdx uint32

func getNewDataForwarderIDX() uint32 {
	return atomic.AddUint32(&dataForwarderIdx, 1)
}

func NewForwarder(locConn net.Conn, mainRemConn *dataFwdRemotePart, isIntentConnection bool, logger *log.Entry) (DataForwarderIf, error) {
	// some sanity checks
	if locConn == nil || mainRemConn == nil || logger == nil {
		return nil, errors.New("Can't process with Empty input arguments")
	}

	forwarder := new(dataForwarder)
	if forwarder == nil {
		panic("Can't allocate new forwarder. Out of memory")
	}

	forwarder.dataForwarderIdx = getNewDataForwarderIDX()
	forwarder.localTcpConn = locConn
	forwarder.intentConnection = isIntentConnection
	forwarder.remConn[0] = mainRemConn
	forwarder.logger = logger

	go forwarder.dataForward()

	return DataForwarderIf(forwarder), nil
}

func (d *dataForwarder) RegisterNewRemotePart(newPartChannel *dataFwdRemotePart) error {
	d.remConnLock.Lock()
	defer d.remConnLock.Unlock()

	// check if remote connection channel already registered
	if d.remConn[1] != nil {
		return errors.New("Additional remote channel already in use, can't register new one")
	}
	if newPartChannel == nil {
		return errors.New("Can't process empty input parameter")
	}

	d.remConn[1] = newPartChannel

	return nil
}

func (d *dataForwarder) GetForwardStates() (upState bool, downState bool) {
	return d.dataForwardInUpstreamTransaction.IsStatusOK(), d.dataForwardInDownstreamTransaction.IsStatusOK()
}

func (d *dataForwarder) StopForward() {
	d.remConnLock.Lock()
	defer d.remConnLock.Unlock()

	d.dataForwardStopped.StatusSet(true)
}

func (d *dataForwarder) StartForward() {
	d.remConnLock.Lock()
	defer d.remConnLock.Unlock()

	d.dataForwardStopped.StatusSet(false)
}

func (d *dataForwarder) dataForward() {
	d.logger.Debugf("Data forwarder %d started", d.dataForwarderIdx)

	// define helper connection type name handler
	connectionTypeName := func() string {
		if d.intentConnection {
			return "Consume"
		} else {
			return "Provide"
		}
	}

	// create WaitGroup for CopyBufferIO
	wg := &sync.WaitGroup{}

	// run channel buffers management
	var once sync.Once

	// Prepare teardown function
	close := func() {
		if d.localTcpConn != nil {
			d.localTcpConn.Close()
		}
		for idx := 0; idx < 2; idx++ {
			if d.remConn[idx] != nil {
				d.remConn[idx].closer.Close()
				d.remConn[idx].forwardStopChannel <- time.Now()
			}
		}

		// waiting both IO buffer copy goroutines exited
		wg.Wait()

		d.logger.Debugf("Data forwarder %d closed", d.dataForwarderIdx)
	}

	// downstream
	wg.Add(1)
	go func() {
		d.logger.Debugf("Downstream %s connection part %s <- %s started",
			connectionTypeName(), d.localTcpConn.RemoteAddr().String(), d.localTcpConn.LocalAddr().String())

		written, errRd, errWr := d.bufCopy(false)
		if errRd != nil || errWr != nil {
			d.logger.Debugf("Donstream %s connection part %s <- %s closed with status: RX %v, TX %v. Total transmitted bytes: %d",
				connectionTypeName(), d.localTcpConn.RemoteAddr().String(), d.localTcpConn.LocalAddr().String(), errRd, errWr, written)
		} else {
			d.logger.Debugf("Downstream %s connection part %s <- %s closed without errors. Total transmitted bytes: %d",
				connectionTypeName(), d.localTcpConn.RemoteAddr().String(), d.localTcpConn.LocalAddr().String(), written)
		}

		// we must not use DEFER here because wg.Wait() runs into close() func,
		// so one of defer() is never fire up because stack will be stocked in close()
		wg.Done()

		once.Do(close)
	}()

	// upstream
	wg.Add(1)
	go func() {
		d.logger.Debugf("Upstream %s connection part %s -> %s started",
			connectionTypeName(), d.localTcpConn.RemoteAddr().String(), d.localTcpConn.LocalAddr().String())

		written, errRd, errWr := d.bufCopy(true)
		if errRd != nil || errWr != nil {
			d.logger.Debugf("Upstream %s connection part %s -> %s closed with status: RX %v, TX %v. Total transmitted bytes: %d",
				connectionTypeName(), d.localTcpConn.RemoteAddr().String(), d.localTcpConn.LocalAddr().String(), errRd, errWr, written)
		} else {
			d.logger.Debugf("Upstream %s connection part %s -> %s closed without errors. Total transmitted bytes: %d",
				connectionTypeName(), d.localTcpConn.RemoteAddr().String(), d.localTcpConn.LocalAddr().String(), written)
		}

		// we must not use DEFER here because wg.Wait() runs into close() func,
		// so one of defer() is never fire up because stack will be stocked in close()
		wg.Done()

		once.Do(close)
	}()
}

func (d *dataForwarder) setTransactionFlag(isUpsatream bool, status bool) {
	// set transaction flag
	if isUpsatream {
		d.dataForwardInUpstreamTransaction.StatusSet(status)
	} else {
		d.dataForwardInDownstreamTransaction.StatusSet(status)
	}
}

// function implements stream copy ability with statistics update
func (d *dataForwarder) bufCopy(isUpsatream bool) (written int64, readerErr error, writerErr error) {
	// create intermediate buffer with size equals to MAX socket datagram size
	buf := make([]byte, 64*1024)

	var reader io.Reader
	var writer io.Writer

	for {
		// we needs to set proper io primitives for each transaction
		// because of it may be changed in paired connection move transaction
		if isUpsatream {
			reader = d.localTcpConn
			writer = d.remConn[0].writer
		} else {
			reader = d.remConn[0].reader
			writer = d.localTcpConn
		}

		// reset transaction flag
		d.setTransactionFlag(isUpsatream, false)

		// stop upstream by external request
		if isUpsatream && d.dataForwardStopped.IsStatusOK() {
			time.Sleep(time.Millisecond)
			continue
		}

		// NOTE: Upstream half reads data from local connection
		// Downstream half reads data from remote connection
		//
		// When Data Forwarder is stopped, data from local connection doesn't processed
		// Socket just locked due to no Rcv API invoked
		//
		// NOTE: Downstream connection works as ordinary, but it's ready to closing remote channel
		// in this case (if error was io.EOF) we just replace remote connection with newly registered
		// and reset stopped forwarding flag
		nr, readerErr := reader.Read(buf)
		if nr > 0 {
			// set transaction flag
			d.setTransactionFlag(isUpsatream, true)

			nw, writerErr := writer.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if writerErr != nil || nr != nw {
				if nr != nw {
					writerErr = io.ErrShortWrite
				}
				break
			}
		}

		if readerErr != nil {
			// if this is downstream half and data forwarder in stopped state
			// and remote channel closed correctly, we replace remote connection with newly registered,
			// sent close signal in parent paired connection of previous primary remote connection
			// and enable forwarding
			if !isUpsatream &&
				d.dataForwardStopped.IsStatusOK() &&
				d.remConn[1] != nil && // other parts must be checked in upper level API
				readerErr == io.EOF {
				// reload remote connectors PTRs
				stopCh := d.remConn[0].forwardStopChannel
				d.remConn[0] = d.remConn[1]
				d.remConn[1] = nil
				stopCh <- time.Now()
				d.dataForwardStopped.StatusSet(false)
			} else {
				break
			}
		}
	}

	return written, readerErr, writerErr
}
