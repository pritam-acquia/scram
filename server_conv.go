package scram

import (
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
)

type serverState int

const (
	serverFirst serverState = iota
	serverFinal
	serverDone
)

// ServerConversation ...
type ServerConversation struct {
	nonceGen     NonceGeneratorFcn
	hashGen      HashGeneratorFcn
	credentialCB CredentialLookup
	state        serverState
	credential   StoredCredentials
	gs2Header    string
	username     string
	authID       string
	nonce        string
	c1b          string
	s1           string
}

// Step ...
func (sc *ServerConversation) Step(challenge string) (response string, err error) {
	switch sc.state {
	case serverFirst:
		sc.state = serverFinal
		response, err = sc.firstMsg(challenge)
	case serverFinal:
		sc.state = serverDone
		response, err = sc.finalMsg(challenge)
	default:
		response, err = "", errors.New("Conversation already completed")
	}
	return
}

// Done ...
func (sc *ServerConversation) Done() bool {
	return sc.state == serverDone
}

// Username ...
func (sc *ServerConversation) Username() string {
	return sc.username
}

// AuthID ...
func (sc *ServerConversation) AuthID() string {
	return sc.authID
}

func (sc *ServerConversation) firstMsg(c1 string) (string, error) {
	msg, err := parseClientFirst(c1)
	if err != nil {
		return "", err
	}

	sc.gs2Header = msg.gs2Header
	sc.username = msg.username
	sc.authID = msg.authID

	sc.credential, err = sc.credentialCB(msg.username)
	if err != nil {
		return "", err
	}

	sc.nonce = msg.nonce + sc.nonceGen()
	sc.c1b = msg.c1b
	sc.s1 = fmt.Sprintf("r=%s,s=%s,i=%d",
		sc.nonce,
		base64.StdEncoding.EncodeToString([]byte(sc.credential.Salt)),
		sc.credential.Iters,
	)

	return sc.s1, nil
}

// For errors, returns server error message as well as non-nil error.  Callers
// can choose whether to send server error or not.
func (sc *ServerConversation) finalMsg(c2 string) (string, error) {
	msg, err := parseClientFinal(c2)
	if err != nil {
		return "", err
	}

	// Check channel binding matches what we expect; in this case, we expect
	// just the gs2 header we received as we don't support channel binding
	// with a data payload.  If we add binding, we need to independently
	// compute the header to match here.
	if string(msg.cbind) != sc.gs2Header {
		return "e=channel-bindings-dont-match", fmt.Errorf("channel binding received '%s' doesn't match expected '%s'", msg.cbind, sc.gs2Header)
	}

	// Check nonce received matches what we sent
	if msg.nonce != sc.nonce {
		return "e=other-error", errors.New("nonce received did not match nonce sent")
	}

	// Create auth message
	authMsg := sc.c1b + "," + sc.s1 + "," + msg.c2wop

	// Retrieve ClientKey from proof and verify it
	clientSignature := computeHMAC(sc.hashGen, sc.credential.StoredKey, []byte(authMsg))
	clientKey := xorBytes([]byte(msg.proof), clientSignature)
	storedKey := computeHash(sc.hashGen, clientKey)

	// Compare with constant-time function
	if !hmac.Equal(storedKey, sc.credential.StoredKey) {
		return "e=invalid-proof", errors.New("challenge proof invalid")
	}

	// Compute and return server verifier
	serverSignature := computeHMAC(sc.hashGen, sc.credential.ServerKey, []byte(authMsg))
	return "v=" + base64.StdEncoding.EncodeToString(serverSignature), nil
}
