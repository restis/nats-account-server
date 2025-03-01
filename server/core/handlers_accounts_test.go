/*
 * Copyright 2019 The NATS Authors
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package core

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt"
	"github.com/nats-io/nats-account-server/server/conf"
	nats "github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"
)

func TestAccountAndAccounts(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, true)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts")
	url := testEnv.URLForPath(path)
	resp, err := http.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	path = fmt.Sprintf("/jwt/v1/accounts/")
	url = testEnv.URLForPath(path)
	resp, err = http.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
}

func TestAccountAndAccountsNewPort(t *testing.T) {
	config := conf.DefaultServerConfig()
	config.HTTP.Port = 14193
	testEnv, err := SetupTestServer(config, false, true)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts")
	url := testEnv.URLForPath(path)
	resp, err := http.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	path = fmt.Sprintf("/jwt/v1/accounts/")
	url = testEnv.URLForPath(path)
	resp, err = http.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
}

func TestUploadGetAccountJWT(t *testing.T) {
	lock := sync.Mutex{}

	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, true)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	operatorKey := testEnv.OperatorKey
	opk := testEnv.OperatorPubKey

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	accountKey2, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey2, err := accountKey2.PublicKey()
	require.NoError(t, err)

	act := jwt.NewActivationClaims(pubKey)
	act.ImportType = jwt.Stream
	act.Name = "times"
	act.ImportSubject = "times.*"
	activeJWT, err := act.Encode(accountKey2)
	require.NoError(t, err)

	imp := jwt.Import{
		Name:    "times",
		Subject: "times.*",
		Account: pubKey2,
		Token:   activeJWT,
		Type:    jwt.Stream,
	}

	expireAt := time.Now().Add(24 * time.Hour).Unix()
	account := jwt.NewAccountClaims(pubKey)
	account.Imports = append(account.Imports, &imp)
	account.Expires = expireAt
	acctJWT, err := account.Encode(operatorKey)
	require.NoError(t, err)
	notificationJWT := ""
	subject := fmt.Sprintf(accountNotificationFormat, pubKey)
	_, err = testEnv.NC.Subscribe(subject, func(m *nats.Msg) {
		lock.Lock()
		notificationJWT = string(m.Data)
		lock.Unlock()
	})
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
	body, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	savedJWT := string(body)

	testEnv.Server.nats.Flush()
	testEnv.NC.Flush()

	lock.Lock()
	require.Equal(t, notificationJWT, string(savedJWT))
	lock.Unlock()

	savedClaims, err := jwt.DecodeAccountClaims(string(savedJWT))
	require.NoError(t, err)

	require.Equal(t, account.Subject, savedClaims.Subject)
	require.Equal(t, opk, savedClaims.Issuer)

	path = fmt.Sprintf("/jwt/v1/accounts/%s?check=true", pubKey)
	url = testEnv.URLForPath(path)
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	path = fmt.Sprintf("/jwt/v1/accounts/%s?text=true", pubKey)
	url = testEnv.URLForPath(path)
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
	body, err = ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(body), "eyJ0eXAiOiJqd3QiLCJhbGciOiJlZDI1NTE5In0")) // header prefix doesn't change

	path = fmt.Sprintf("/jwt/v1/accounts/%s?decode=true", pubKey)
	url = testEnv.URLForPath(path)
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
	body, err = ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	decoded := string(body)
	require.True(t, strings.Contains(decoded, `"alg": "ed25519"`))          // header prefix doesn't change
	require.True(t, strings.Contains(decoded, UnixToDate(int64(expireAt)))) // expires are resolved to readable form
	require.True(t, strings.Contains(decoded, "times.*"))                   // activation token is decoded

	notificationJWT = ""

	path = fmt.Sprintf("/jwt/v1/accounts/%s?notify=true", pubKey)
	url = testEnv.URLForPath(path)
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
	testEnv.Server.nats.Flush()
	testEnv.NC.Flush()

	lock.Lock()
	require.Equal(t, notificationJWT, string(savedJWT))
	lock.Unlock()
}

func TestUnknownURL(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, true)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/biz")
	url := testEnv.URLForPath(path)
	resp, err := testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusNotFound)

	path = fmt.Sprintf("/biz/v1/accounts/")
	url = testEnv.URLForPath(path)
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusNotFound)
}

func TestExpiredJWT(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, true)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	operatorKey := testEnv.OperatorKey

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	account := jwt.NewAccountClaims(pubKey)
	account.Expires = time.Now().Unix() - 10000
	acctJWT, err := account.Encode(operatorKey)
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusBadRequest) // Already expired
	body, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	message := string(body)

	require.True(t, strings.Contains(message, "expired"))

	account = jwt.NewAccountClaims(pubKey)
	account.Expires = time.Now().Unix() + 2
	acctJWT, err = account.Encode(operatorKey)
	require.NoError(t, err)

	// expire in a few seconds
	resp, err = testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	// Hate to sleep, but need to let it expire
	time.Sleep(time.Second * 3)

	// Get doesn't check expires by default
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	// check flag will force the expires check
	path = fmt.Sprintf("/jwt/v1/accounts/%s?check=true", pubKey)
	url = testEnv.URLForPath(path)
	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusNotFound)
}

func TestUploadGetAccountJWTTLS(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), true, false)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	operatorKey := testEnv.OperatorKey

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	account := jwt.NewAccountClaims(pubKey)
	acctJWT, err := account.Encode(operatorKey)
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)
}

func TestInvalidJWTPost(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, false)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte("hello world")))
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)
}

func TestInvalidSigner(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, false)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	account := jwt.NewAccountClaims(pubKey)
	acctJWT, err := account.Encode(accountKey)
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)
}

func TestUnknownSigner(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, false)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	operatorKey, err := nkeys.CreateOperator()
	require.NoError(t, err)

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	account := jwt.NewAccountClaims(pubKey)
	acctJWT, err := account.Encode(operatorKey)
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)
}

func TestExpiredAccount(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, false)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	operatorKey := testEnv.OperatorKey

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	account := jwt.NewAccountClaims(pubKey)
	account.Expires = time.Now().Unix() - 1000
	acctJWT, err := account.Encode(operatorKey)
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)
}

func TestCacheHeader(t *testing.T) {
	testEnv, err := SetupTestServer(conf.DefaultServerConfig(), false, false)
	defer testEnv.Cleanup()
	require.NoError(t, err)

	operatorKey := testEnv.OperatorKey

	accountKey, err := nkeys.CreateAccount()
	require.NoError(t, err)

	pubKey, err := accountKey.PublicKey()
	require.NoError(t, err)

	account := jwt.NewAccountClaims(pubKey)
	acctJWT, err := account.Encode(operatorKey)
	require.NoError(t, err)

	path := fmt.Sprintf("/jwt/v1/accounts/%s", pubKey)
	url := testEnv.URLForPath(path)

	resp, err := testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.False(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Post(url, "application/json", bytes.NewBuffer([]byte(acctJWT)))
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	resp, err = testEnv.HTTP.Get(url)
	require.NoError(t, err)
	require.True(t, resp.StatusCode == http.StatusOK)

	etag := resp.Header["Etag"][0]

	request, err := http.NewRequest("GET", url, nil)
	require.NoError(t, err)
	request.Header.Set("If-None-Match", etag)

	resp, err = testEnv.HTTP.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotModified, resp.StatusCode)
}
