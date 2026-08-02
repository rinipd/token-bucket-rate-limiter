package limiter

import "testing"

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid token bucket config",
			cfg:     Config{RPS: 1, Burst: 5, Algorithm: AlgorithmTokenBucket},
			wantErr: false,
		},
		{
			name:    "valid sliding window config",
			cfg:     Config{RPS: 1, Burst: 5, Algorithm: AlgorithmSlidingWindow},
			wantErr: false,
		},
		{
			name:    "negative RPS",
			cfg:     Config{RPS: -1, Burst: 5, Algorithm: AlgorithmTokenBucket},
			wantErr: true,
		},
		{
			name:    "zero RPS",
			cfg:     Config{RPS: 0, Burst: 5, Algorithm: AlgorithmTokenBucket},
			wantErr: true,
		},
		{
			name:    "zero burst",
			cfg:     Config{RPS: 1, Burst: 0, Algorithm: AlgorithmTokenBucket},
			wantErr: true,
		},
		{
			name:    "unknown algorithm",
			cfg:     Config{RPS: 1, Burst: 5, Algorithm: "leaky_bucket"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
