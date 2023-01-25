package main

import (
	"bytes"
	"crypto"
	"crypto/liboqs_sig"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/big"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	err := main2()
	if err != nil {
		log.Fatal(err)
	}
}

type issuer struct {
	key interface{}
	cert *x509.Certificate
}

func getIssuer(keyFile, certFile, signatureScheme string) (*issuer, error) {
	keyContents, keyErr := ioutil.ReadFile(keyFile)
	certContents, certErr := ioutil.ReadFile(certFile)
	if os.IsNotExist(keyErr) && os.IsNotExist(certErr) {
		err := makeIssuer(keyFile, certFile, signatureScheme)
		if err != nil {
			return nil, err
		}
		return getIssuer(keyFile, certFile, signatureScheme)
	} else if keyErr != nil {
		return nil, fmt.Errorf("%s (but %s exists)", keyErr, certFile)
	} else if certErr != nil {
		return nil, fmt.Errorf("%s (but %s exists)", certErr, keyFile)
	}

	rsa, pqc, err := readPrivateKey(keyContents)

	if err != nil {
		return nil, fmt.Errorf("reading private key from %s: %s", keyFile, err)
	}

	cert, err := readCert(certContents)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate from %s: %s", certFile, err)
	}
	if rsa !=  nil {
		equal, err := publicKeysEqual(rsa.Public(), cert.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("comparing public keys: %s", err)
		} else if !equal {
			return nil, fmt.Errorf("public key in CA certificate %s doesn't match private key in %s",
				certFile, keyFile)
		}
		return &issuer{rsa, cert}, nil
	} else if pqc != nil {
		return &issuer{pqc, cert}, nil
	}
	return nil, nil
	
}

func readPrivateKey(keyContents []byte) (crypto.Signer, interface{}, error) {
	block, _ := pem.Decode(keyContents)
	if block == nil {
		return nil, nil,  fmt.Errorf("no PEM found")
	} else if block.Type == "RSA PRIVATE KEY" || block.Type == "ECDSA PRIVATE KEY" {
		priv, err :=  x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("Classic: Unmarshall problem 2")
		}
		return priv, nil, nil
		
	} else if block.Type == "PQC PRIVATE KEY" {
		priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil,  fmt.Errorf("PQC: Unmarshall problem 2")
		}
		return nil, priv, nil
	
	} else {
		return nil, nil, fmt.Errorf("incorrect PEM type %s", block.Type)
	}
	
}

func readCert(certContents []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certContents)
	if block == nil {
		return nil, fmt.Errorf("no PEM found")
	} else if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("incorrect PEM type %s", block.Type)
	}
	return x509.ParseCertificate(block.Bytes)
}

func makeIssuer(keyFile, certFile, signatureScheme string) error {
	rsa, pqc, err := makeKey(keyFile, signatureScheme)
	if err != nil {
		return err
	}
	if signatureScheme == "RSA" {
		_, err = makeRootCert(rsa, certFile)
		if err != nil {
			return err
		}
	} else if signatureScheme == "Dilithium2" || signatureScheme == "Falcon-512" || signatureScheme == "sphincs+-SHAKE256-256s-simple"{
		_, err = makePQCRootCert(pqc, certFile)
		if err != nil {
			return err
		}
	}
	return nil
}

func makeKey(filename, signatureScheme string) (*rsa.PrivateKey, *liboqs_sig.PrivateKey, error) {
	
	if signatureScheme == "SHA256-RSA"{
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, nil, err
		}
		der := x509.MarshalPKCS1PrivateKey(key)
		if err != nil {
			return nil, nil, err
		}
		file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, nil, err
		}
		defer file.Close()
		err = pem.Encode(file, &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: der,
		})
		if err != nil {
			return nil, nil, err
		}
		return key, nil, nil
	
	} else if signatureScheme == "Dilithium2" || signatureScheme == "Falcon-512" || signatureScheme == "sphincs+-SHAKE256-256s-simple" {
		ID := liboqs_sig.NameToSigID(signatureScheme)
		_, key,  err := liboqs_sig.GenerateKey(ID)
		byteKey, err := x509.MarshalPKCS8PrivateKey(key)
		pemKey := &pem.Block{
			Type: "PQC PRIVATE KEY",
			Bytes: byteKey,
		}
		
		file, err := os.OpenFile("minica-pebble/"+filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, nil, err
		}
		defer file.Close()
		err = pem.Encode(file, pemKey)
 		if err != nil {
 			return nil, nil, err
 		}
		// key.Sign()
		return nil, key, nil

	}
	return nil, nil, nil
}

func makeRootCert(key crypto.Signer, filename string) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	skid, err := calculateSKID(key.Public())
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "minica root ca " + hex.EncodeToString(serial.Bytes()[:3]),
		},
		SerialNumber: serial,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(100, 0, 0),

		SubjectKeyId:          skid,
		AuthorityKeyId:        skid,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	err = pem.Encode(file, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func makePQCRootCert(key *liboqs_sig.PrivateKey, filename string) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	skid, err := calculateSKID(key.Public())
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "minica root ca " + hex.EncodeToString(serial.Bytes()[:3]),
		},
		SerialNumber: serial,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(100, 0, 0),

		SubjectKeyId:          skid,
		AuthorityKeyId:        skid,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile("minica-pebble/"+filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	err = pem.Encode(file, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func parseIPs(ipAddresses []string) ([]net.IP, error) {
	var parsed []net.IP
	for _, s := range ipAddresses {
		p := net.ParseIP(s)
		if p == nil {
			return nil, fmt.Errorf("invalid IP address %s", s)
		}
		parsed = append(parsed, p)
	}
	return parsed, nil
}

func publicKeysEqual(a, b interface{}) (bool, error) {
	aBytes, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false, err
	}
	bBytes, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false, err
	}
	return bytes.Compare(aBytes, bBytes) == 0, nil
}

func calculateSKID(pubKey crypto.PublicKey) ([]byte, error) {
	spkiASN1, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return nil, err
	}

	var spki struct {
		Algorithm        pkix.AlgorithmIdentifier
		SubjectPublicKey asn1.BitString
	}
	_, err = asn1.Unmarshal(spkiASN1, &spki)
	if err != nil {
		return nil, err
	}
	skid := sha1.Sum(spki.SubjectPublicKey.Bytes)
	return skid[:], nil
}

func sign(iss *issuer, domains []string, ipAddresses []string, pebbleSig string) (*x509.Certificate, error) {
	var cn string
	if len(domains) > 0 {
		cn = domains[0]
	} else if len(ipAddresses) > 0 {
		cn = ipAddresses[0]
	} else {
		return nil, fmt.Errorf("must specify at least one domain name or IP address")
	}
	var cnFolder = strings.Replace(cn, "*", "_", -1)
	err := os.Mkdir(cnFolder, 0700)
	if err != nil && !os.IsExist(err) {
		return nil, err
	}
	// 
	rsa, pqc, err := makeKey(fmt.Sprintf("%s/%s.%s.key.pem", cnFolder, strings.ToLower(iss.cert.SignatureAlgorithm.String()), strings.ToLower(pebbleSig)), pebbleSig)
	if err != nil {
		return nil, err
	}
	parsedIPs, err := parseIPs(ipAddresses)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		DNSNames:    domains,
		IPAddresses: parsedIPs,
		Subject: pkix.Name{
			CommonName: cn,
		},
		SerialNumber: serial,
		NotBefore:    time.Now(),
		// Set the validity period to 2 years and 30 days, to satisfy the iOS and
		// macOS requirements that all server certificates must have validity
		// shorter than 825 days:
		// https://derflounder.wordpress.com/2019/06/06/new-tls-security-requirements-for-ios-13-and-macos-catalina-10-15/
		NotAfter: time.Now().AddDate(2, 0, 30),

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	var der []byte
	if rsa == nil {
		der, err = x509.CreateCertificate(rand.Reader, template, iss.cert, pqc.Public(), iss.key)
		if err != nil {
			return nil, err
		}
	} else {
		der, err = x509.CreateCertificate(rand.Reader, template, iss.cert, rsa.Public(), iss.key)
		if err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(fmt.Sprintf("%s/%s.%s.cert.pem", cnFolder, strings.ToLower(iss.cert.SignatureAlgorithm.String()), strings.ToLower(pebbleSig)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	err = pem.Encode(file, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func split(s string) (results []string) {
	if len(s) > 0 {
		return strings.Split(s, ",")
	}
	return nil
}

func main2() error {
	var caKey = flag.String("ca-key", "minica.key", "Root private key filename, PEM encoded.")
	var caCert = flag.String("ca-cert", "minica.pem", "Root certificate filename, PEM encoded.")
	var domains = flag.String("domains", "", "Comma separated domain names to include as Server Alternative Names.")
	var ipAddresses = flag.String("ip-addresses", "", "Comma separated IP addresses to include as Server Alternative Names.")
	var caSig = flag.String("ca-sig", "RSA", "Signature scheme used for sign minica ca")
	var pebbleSig = flag.String("pebble-sig", "RSA", "Signature scheme used for sign pebble cert")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, `
Minica is a simple CA intended for use in situations where the CA operator
also operates each host where a certificate will be used. It automatically
generates both a key and a certificate when asked to produce a certificate.
It does not offer OCSP or CRL services. Minica is appropriate, for instance,
for generating certificates for RPC systems or microservices.

On first run, minica will generate a keypair and a root certificate in the
current directory, and will reuse that same keypair and root certificate
unless they are deleted.

On each run, minica will generate a new keypair and sign an end-entity (leaf)
certificate for that keypair. The certificate will contain a list of DNS names
and/or IP addresses from the command line flags. The key and certificate are
placed in a new directory whose name is chosen as the first domain name from
the certificate, or the first IP address if no domain names are present. It
will not overwrite existing keys or certificates.

`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *domains == "" && *ipAddresses == "" {
		flag.Usage()
		os.Exit(1)
	}
	if len(flag.Args()) > 0 {
		fmt.Printf("Extra arguments: %s (maybe there are spaces in your domain list?)\n", flag.Args())
		os.Exit(1)
	}
	domainSlice := split(*domains)
	domainRe := regexp.MustCompile("^[A-Za-z0-9.*-]+$")
	for _, d := range domainSlice {
		if !domainRe.MatchString(d) {
			fmt.Printf("Invalid domain name %q\n", d)
			os.Exit(1)
		}
	}
	ipSlice := split(*ipAddresses)
	for _, ip := range ipSlice {
		if net.ParseIP(ip) == nil {
			fmt.Printf("Invalid IP address %q\n", ip)
			os.Exit(1)
		}
	}
	issuer, err := getIssuer(*caKey, *caCert, *caSig)
	if err != nil {
 		return err
	}
	_, err = sign(issuer, domainSlice, ipSlice, *pebbleSig)
	return err
}
