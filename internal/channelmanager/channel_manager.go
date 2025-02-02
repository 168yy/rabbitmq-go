package channelmanager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/168yy/rabbitmq-go/internal/connectionmanager"
	"github.com/168yy/rabbitmq-go/internal/dispatcher"
	"github.com/168yy/rabbitmq-go/internal/logger"
	amqp "github.com/rabbitmq/amqp091-go"
)

// ChannelManager -
type ChannelManager struct {
	logger               logger.Logger
	channel              *amqp.Channel
	connManager          *connectionmanager.ConnectionManager
	channelMux           *sync.RWMutex
	reconnectInterval    time.Duration
	reconnectionCount    uint
	reconnectionCountMux *sync.Mutex
	dispatcher           *dispatcher.Dispatcher
}

// NewChannelManager creates a new connection manager
func NewChannelManager(ctx context.Context, connManager *connectionmanager.ConnectionManager, log logger.Logger, reconnectInterval time.Duration) (*ChannelManager, error) {
	ch, err := getNewChannel(connManager)
	if err != nil {
		return nil, err
	}

	chanManager := ChannelManager{
		logger:               log,
		connManager:          connManager,
		channel:              ch,
		channelMux:           &sync.RWMutex{},
		reconnectInterval:    reconnectInterval,
		reconnectionCount:    0,
		reconnectionCountMux: &sync.Mutex{},
		dispatcher:           dispatcher.NewDispatcher(),
	}
	go chanManager.startNotifyCancelOrClosed(ctx)
	return &chanManager, nil
}

func getNewChannel(connManager *connectionmanager.ConnectionManager) (*amqp.Channel, error) {
	conn := connManager.CheckoutConnection()
	defer connManager.CheckinConnection()

	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// startNotifyCancelOrClosed listens on the channel's cancelled and closed
// notifiers. When it detects a problem, it attempts to reconnect.
// Once reconnected, it sends an error back on the manager's notifyCancelOrClose
// channel
func (chanManager *ChannelManager) startNotifyCancelOrClosed(ctx context.Context) {
	notifyCloseChan := chanManager.channel.NotifyClose(make(chan *amqp.Error, 1))
	notifyCancelChan := chanManager.channel.NotifyCancel(make(chan string, 1))

	select {
	case err := <-notifyCloseChan:
		if err != nil {
			chanManager.logger.Errorf(ctx, "attempting to reconnect to amqp server after close with error: %v", err)
			chanManager.reconnectLoop(ctx)
			chanManager.logger.Warningf(ctx, "successfully reconnected to amqp server")
			chanManager.dispatcher.Dispatch(err)
		}
		if err == nil {
			chanManager.logger.Infof(ctx, "amqp channel closed gracefully")
		}
	case err := <-notifyCancelChan:
		chanManager.logger.Errorf(ctx, "attempting to reconnect to amqp server after cancel with error: %s", err)
		chanManager.reconnectLoop(ctx)
		chanManager.logger.Warningf(ctx, "successfully reconnected to amqp server after cancel")
		chanManager.dispatcher.Dispatch(errors.New(err))
	}
}

// GetReconnectionCount -
func (chanManager *ChannelManager) GetReconnectionCount() uint {
	chanManager.reconnectionCountMux.Lock()
	defer chanManager.reconnectionCountMux.Unlock()
	return chanManager.reconnectionCount
}

func (chanManager *ChannelManager) incrementReconnectionCount() {
	chanManager.reconnectionCountMux.Lock()
	defer chanManager.reconnectionCountMux.Unlock()
	chanManager.reconnectionCount++
}

// reconnectLoop continuously attempts to reconnect
func (chanManager *ChannelManager) reconnectLoop(ctx context.Context) {
	for {
		chanManager.logger.Infof(ctx, "waiting %s seconds to attempt to reconnect to amqp server", chanManager.reconnectInterval)
		time.Sleep(chanManager.reconnectInterval)
		err := chanManager.reconnect(ctx)
		if err != nil {
			chanManager.logger.Errorf(ctx, "error reconnecting to amqp server: %v", err)
		} else {
			chanManager.incrementReconnectionCount()
			go chanManager.startNotifyCancelOrClosed(ctx)
			return
		}
	}
}

// reconnect safely closes the current channel and obtains a new one
func (chanManager *ChannelManager) reconnect(ctx context.Context) error {
	chanManager.channelMux.Lock()
	defer chanManager.channelMux.Unlock()
	newChannel, err := getNewChannel(chanManager.connManager)
	if err != nil {
		return err
	}

	if err = chanManager.channel.Close(); err != nil {
		chanManager.logger.Warningf(ctx, "error closing channel while reconnecting: %v", err)
	}

	chanManager.channel = newChannel
	return nil
}

// Close safely closes the current channel and connection
func (chanManager *ChannelManager) Close(ctx context.Context) error {
	chanManager.logger.Infof(ctx, "closing channel manager...")
	chanManager.channelMux.Lock()
	defer chanManager.channelMux.Unlock()

	err := chanManager.channel.Close()
	if err != nil {
		return err
	}

	return nil
}

// NotifyReconnect adds a new subscriber that will receive error messages whenever
// the connection manager has successfully reconnect to the server
func (chanManager *ChannelManager) NotifyReconnect() (<-chan error, chan<- struct{}) {
	return chanManager.dispatcher.AddSubscriber()
}
