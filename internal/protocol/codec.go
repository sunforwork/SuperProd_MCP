package protocol

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

const MaxPlaintext = 64 << 20

var prefix = regexp.MustCompile(`^pf_(C)?(E)?([0-9]+(?:\.[0-9]+)?)__`)
var ErrPassword = errors.New("sync decryption failed: missing/wrong password or damaged ciphertext")

type Format struct {
	Compressed bool
	Encrypted  bool
	Version    string
}

type Codec struct {
	password []byte
	mu       sync.Mutex
	keys     map[string][]byte
	salt     []byte
}

func NewCodec(password string) *Codec {
	return &Codec{password: []byte(password), keys: map[string][]byte{}}
}
func (c *Codec) key(salt []byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := string(salt)
	if key, ok := c.keys[s]; ok {
		return key
	}
	key := argon2.IDKey(c.password, salt, 3, 65536, 1, 32)
	if len(c.keys) >= 4 {
		c.keys = map[string][]byte{}
	}
	c.keys[s] = key
	return key
}
func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (c *Codec) decrypt(data []byte) ([]byte, error) {
	if len(c.password) == 0 || len(data) < 28 {
		return nil, ErrPassword
	}
	if len(data) >= 44 {
		a, err := gcm(c.key(data[:16]))
		if err != nil {
			return nil, err
		}
		if out, err := a.Open(nil, data[16:28], data[28:], nil); err == nil {
			return out, nil
		}
	}

	key := pbkdf2.Key(c.password, c.password, 1000, 32, sha256.New)
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	out, err := a.Open(nil, data[:12], data[12:], nil)
	if err != nil {
		return nil, ErrPassword
	}
	return out, nil
}
func (c *Codec) Decode(data []byte, requireEncrypted bool) (Object, Format, error) {
	m := prefix.FindSubmatch(data)
	if m == nil {
		return nil, Format{}, errors.New("invalid sync prefix")
	}
	f := Format{len(m[1]) > 0, len(m[2]) > 0, string(m[3])}
	if requireEncrypted && !f.Encrypted {
		return nil, f, errors.New("encrypted sync expected; refusing plaintext")
	}
	body := data[len(m[0]):]
	if f.Encrypted {
		b, err := base64.StdEncoding.DecodeString(string(body))
		if err != nil {
			return nil, f, ErrPassword
		}
		body, err = c.decrypt(b)
		if err != nil {
			return nil, f, err
		}
	}
	if f.Compressed {
		b, err := base64.StdEncoding.DecodeString(string(body))
		if err != nil {
			return nil, f, errors.New("invalid gzip base64")
		}
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, f, errors.New("invalid gzip stream")
		}
		defer r.Close()
		body, err = io.ReadAll(io.LimitReader(r, MaxPlaintext+1))
		if err != nil {
			return nil, f, errors.New("corrupt gzip stream")
		}
	}
	if len(body) > MaxPlaintext {
		return nil, f, errors.New("decoded document too large")
	}
	obj, err := Parse(body)
	return obj, f, err
}
func (c *Codec) Encode(obj Object, f Format) ([]byte, error) {
	if !regexp.MustCompile(`^\d+(?:\.\d+)?$`).MatchString(f.Version) {
		return nil, errors.New("invalid format version")
	}
	body, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	flags := ""
	if f.Compressed {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err = z.Write(body); err != nil {
			return nil, err
		}
		if err = z.Close(); err != nil {
			return nil, err
		}
		body = []byte(base64.StdEncoding.EncodeToString(b.Bytes()))
		flags += "C"
	}
	if f.Encrypted {
		if len(c.password) == 0 {
			return nil, ErrPassword
		}
		c.mu.Lock()
		if c.salt == nil {
			c.salt = make([]byte, 16)
			if _, err = rand.Read(c.salt); err != nil {
				c.mu.Unlock()
				return nil, err
			}
		}
		salt := bytes.Clone(c.salt)
		c.mu.Unlock()
		a, err := gcm(c.key(salt))
		if err != nil {
			return nil, err
		}
		iv := make([]byte, 12)
		if _, err = rand.Read(iv); err != nil {
			return nil, err
		}
		out := append(salt, iv...)
		out = a.Seal(out, iv, body, nil)
		body = []byte(base64.StdEncoding.EncodeToString(out))
		flags += "E"
	}
	return append([]byte(fmt.Sprintf("pf_%s%s__", flags, f.Version)), body...), nil
}
