package ysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"

	_ "github.com/lib/pq"
)

// YugabyteConnectionProducer implements ConnectionProducer and provides a generic producer for most sql databases
type YugabyteConnectionProducer struct {
	ConnectionURL            string      `json:"connection_url" mapstructure:"connection_url" structs:"connection_url"`
	MaxOpenConnections       int         `json:"max_open_connections" mapstructure:"max_open_connections" structs:"max_open_connections"`
	MaxIdleConnections       int         `json:"max_idle_connections" mapstructure:"max_idle_connections" structs:"max_idle_connections"`
	MaxConnectionLifetimeRaw interface{} `json:"max_connection_lifetime" mapstructure:"max_connection_lifetime" structs:"max_connection_lifetime"`
	Host                     string      `json:"host" mapstructure:"host" structs:"host"`
	Username                 string      `json:"username" mapstructure:"username" structs:"username"`
	Password                 string      `json:"password" mapstructure:"password" structs:"password"`
	Port                     int         `json:"port" mapstructure:"port" structs:"port"`
	DbName                   string      `json:"db" mapstructure:"db" structs:"db"`

	Type                  string
	RawConfig             map[string]interface{}
	maxConnectionLifetime time.Duration
	Initialized           bool
	db                    *sql.DB
	sync.Mutex
}

var ErrNotInitialized = errors.New("connection has not been initialized")

func (c *YugabyteConnectionProducer) Initialize(ctx context.Context, conf map[string]interface{}, verifyConnection bool) error {
	_, err := c.Init(ctx, conf, verifyConnection)
	return err
}

func (c *YugabyteConnectionProducer) Init(ctx context.Context, conf map[string]interface{}, verifyConnection bool) (map[string]interface{}, error) {
	c.Lock()
	defer c.Unlock()

	c.RawConfig = conf

	decoderConfig := &mapstructure.DecoderConfig{
		Result:           c,
		WeaklyTypedInput: true,
		TagName:          "json",
	}

	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return nil, err
	}

	err = decoder.Decode(conf)
	if err != nil {
		return nil, err
	}

	switch {
	case len(c.ConnectionURL) != 0:
		break //As the connection will be produced through it
	case len(c.Host) == 0:
		return nil, fmt.Errorf("host cannot be empty")
	case len(c.Username) == 0:
		return nil, fmt.Errorf("username cannot be empty")
	case len(c.Password) == 0:
		return nil, fmt.Errorf("password cannot be empty")
	}

	// Set initialized to true at this point since all fields are set,
	// and the connection can be established at a later time.
	c.Initialized = true

	if verifyConnection {
		if _, err := c.Connection(ctx); err != nil {
			return nil, fmt.Errorf("error verifying connection: %s", err)
		}

		if err := c.db.PingContext(ctx); err != nil {
			return nil, fmt.Errorf("error verifying connection: %s", err)
		}
	}

	return c.RawConfig, nil
}

func (c *YugabyteConnectionProducer) Connection(ctx context.Context) (interface{}, error) {
	if !c.Initialized {
		return nil, ErrNotInitialized
	}

	// If we already have a DB, test it and return
	if c.db != nil {
		if err := c.db.PingContext(ctx); err == nil {
			return c.db, nil
		}
		// If the ping was unsuccessful, close it and ignore errors as we'll be
		// reestablishing anyways
		c.db.Close()
	}

	//attempt to make connection
	conn := c.ConnectionURL

	// Ensure timezone is set to UTC for all the connections
	if strings.Contains(conn, "?") {
		conn += "&timezone=UTC"
	} else {
		conn += "?timezone=UTC"
	}

	// Ensure a reasonable application_name is set
	if !strings.Contains(conn, "application_name") {
		conn += "&application_name=vault"
	}

	var err error
	c.db, err = sql.Open("postgres", conn)
	if err != nil {
		return nil, err
	}

	// Set some connection pool settings. We don't need much of this,
	// since the request rate shouldn't be high.
	c.db.SetMaxOpenConns(c.MaxOpenConnections)
	c.db.SetMaxIdleConns(c.MaxIdleConnections)
	c.db.SetConnMaxLifetime(c.maxConnectionLifetime)

	return c.db, nil
}

// Close attempts to close the connection
func (c *YugabyteConnectionProducer) Close() error {
	// Grab the write lock
	c.Lock()
	defer c.Unlock()

	if c.db != nil {
		c.db.Close()
	}

	c.db = nil

	return nil
}
