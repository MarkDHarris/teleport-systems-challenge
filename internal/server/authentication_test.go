package server

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
)

// test getting identity from certificate
func TestIdentityFromCert(t *testing.T) {
	tests := []struct {
		name      string
		cert      *x509.Certificate
		wantCN    string
		wantAdmin bool
		wantErr   bool
	}{
		{
			name: "regular user",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName:         "alice",
					OrganizationalUnit: []string{"eng"},
				},
			},
			wantCN:    "alice",
			wantAdmin: false,
		},
		{
			name: "admin user",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName:         "admin-ops",
					OrganizationalUnit: []string{"admin"},
				},
			},
			wantCN:    "admin-ops",
			wantAdmin: true,
		},
		{
			name: "admin in multiple OUs",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName:         "superuser",
					OrganizationalUnit: []string{"eng", "admin"},
				},
			},
			wantCN:    "superuser",
			wantAdmin: true,
		},
		{
			name: "no OU",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName: "service-bot",
				},
			},
			wantCN:    "service-bot",
			wantAdmin: false,
		},
		{
			name: "empty CN rejected",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName:         "",
					OrganizationalUnit: []string{"eng"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := identityFromCertificate(tt.cert)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if id.CN != tt.wantCN {
				t.Errorf("CN = %q, want %q", id.CN, tt.wantCN)
			}
			if id.IsAdmin != tt.wantAdmin {
				t.Errorf("IsAdmin = %v, want %v", id.IsAdmin, tt.wantAdmin)
			}
		})
	}
}

// test authorization
func TestAuthorize(t *testing.T) {
	tests := []struct {
		name     string
		caller   callerIdentity
		jobOwner string
		wantErr  bool
	}{
		{
			name:     "owner can access own job",
			caller:   callerIdentity{CN: "alice", IsAdmin: false},
			jobOwner: "alice",
			wantErr:  false,
		},
		{
			name:     "admin can access any job",
			caller:   callerIdentity{CN: "admin-ops", IsAdmin: true},
			jobOwner: "alice",
			wantErr:  false,
		},
		{
			name:     "non-owner non-admin denied",
			caller:   callerIdentity{CN: "bob", IsAdmin: false},
			jobOwner: "alice",
			wantErr:  true,
		},
		{
			name:     "admin who also happens to be owner",
			caller:   callerIdentity{CN: "alice", IsAdmin: true},
			jobOwner: "alice",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := authorize(tt.caller, tt.jobOwner)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
