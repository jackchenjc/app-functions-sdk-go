//
// Copyright (c) 2021 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package secure

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/messaging"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/secret"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"

	"github.com/eclipse/paho.mqtt.golang"
)

type MqttFactory struct {
	sp                messaging.SecretDataProvider
	logger            logger.LoggingClient
	authMode          string
	secretPath        string
	opts              *mqtt.ClientOptions
	skipCertVerify    bool
	secretAddedSignal chan struct{}
}

func NewMqttFactory(sp messaging.SecretDataProvider, log logger.LoggingClient, mode string, path string, skipVerify bool,
	secretAddedSignal chan struct{}) MqttFactory {
	return MqttFactory{
		sp:                sp,
		logger:            log,
		authMode:          mode,
		secretPath:        path,
		skipCertVerify:    skipVerify,
		secretAddedSignal: secretAddedSignal,
	}
}

func (factory MqttFactory) Create(opts *mqtt.ClientOptions) (mqtt.Client, error) {
	if factory.authMode == "" {
		factory.authMode = messaging.AuthModeNone
		factory.logger.Warn("AuthMode not set, defaulting to \"" + messaging.AuthModeNone + "\"")
	}

	factory.opts = opts

	secretData, err := factory.getValidSecretData()
	switch secret.IsSecurityEnabled() {
	case true:
		if err == nil {
			break
		}
		factory.logger.Error(err.Error())
		for {
			factory.logger.Info("Waiting for the secret creation API call to seed the proper credentials...")
			<-factory.secretAddedSignal
			secretData, err = factory.getValidSecretData()
			if err != nil {
				factory.logger.Error(err.Error())
			} else {
				break
			}
		}
	case false:
		if err != nil {
			return nil, err
		}
	}

	err = factory.configureMQTTClientForAuth(secretData)
	if err != nil {
		return nil, err
	}

	return mqtt.NewClient(factory.opts), nil
}

func (factory MqttFactory) configureMQTTClientForAuth(secretData *messaging.SecretData) error {
	var cert tls.Certificate
	var err error
	caCertPool := x509.NewCertPool()
	tlsConfig := &tls.Config{
		// nolint: gosec
		InsecureSkipVerify: factory.skipCertVerify,
		MinVersion:         tls.VersionTLS12,
	}
	switch factory.authMode {
	case messaging.AuthModeUsernamePassword:
		factory.opts.SetUsername(secretData.Username)
		factory.opts.SetPassword(secretData.Password)
	case messaging.AuthModeCert:
		cert, err = tls.X509KeyPair(secretData.CertPemBlock, secretData.KeyPemBlock)
		if err != nil {
			return err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	case messaging.AuthModeCA:
		// Nothing to do here for this option
	case messaging.AuthModeNone:
		return nil
	}

	if len(secretData.CaPemBlock) > 0 {
		ok := caCertPool.AppendCertsFromPEM(secretData.CaPemBlock)
		if !ok {
			return errors.New("Error parsing CA PEM block")
		}
		tlsConfig.ClientCAs = caCertPool
	}

	factory.opts.SetTLSConfig(tlsConfig)

	return nil
}

func (factory MqttFactory) getValidSecretData() (*messaging.SecretData, error) {
	//get the secrets from the secret provider and populate the struct
	secretData, err := messaging.GetSecretData(factory.authMode, factory.secretPath, factory.sp)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret data from the secret provider, error: %s", err)
	}
	if secretData == nil {
		return nil, nil
	}
	//ensure that the authmode selected has the required secret values
	err = messaging.ValidateSecretData(factory.authMode, factory.secretPath, secretData)
	if err != nil {
		return nil, fmt.Errorf("invalid secret data, error: %s", err)
	} else {
		return secretData, nil
	}
}
