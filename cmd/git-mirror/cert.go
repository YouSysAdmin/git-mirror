package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"git-mirror/internal/config"
	"git-mirror/internal/tlsconf"
)

// cmdCert prints the certificate the HTTPS endpoint presents so clients can
// pin it (self-signed or manual mode).
func cmdCert() *cobra.Command {
	var fingerprint bool
	c := &cobra.Command{
		Use:   "cert",
		Short: "Print the TLS certificate of the git HTTPS endpoint (PEM)",
		Long: `Print the certificate served by the git HTTPS endpoint so clients can trust
it, e.g. for a self-signed setup:

  git-mirror cert > mirror.pem            # on the server, or from any client:
  curl -sk https://mirror:8443/-/cert.pem > mirror.pem
  git -c http.sslCAInfo=mirror.pem clone https://git:<token>@mirror:8443/<name>.git`,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load(theApp.Dir)
			if err != nil {
				return err
			}
			pemData, err := tlsconf.ServingCertPEM(cfg.Settings, theApp.Dir)
			if err != nil {
				return err
			}
			if fingerprint {
				leaf, err := tlsconf.LeafPEM(pemData)
				if err != nil {
					return err
				}
				fp, err := tlsconf.Fingerprint(leaf)
				if err != nil {
					return err
				}
				fmt.Println("SHA256:" + fp)
				return nil
			}
			_, err = os.Stdout.Write(pemData)
			return err
		},
	}
	c.Flags().BoolVar(&fingerprint, "fingerprint", false, "print the SHA-256 fingerprint instead of the PEM")
	return c
}
