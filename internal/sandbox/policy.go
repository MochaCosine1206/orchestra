package sandbox

// Policy combines mount, network, and environment policies into a single container creation spec.
type Policy struct {
	Mount   MountPolicy
	Network *NetworkPolicy
	EnvVars map[string]string // environment variables to pass into container
}

// DockerArgs returns the combined docker run flags for mount, network, and env policies.
func (p Policy) DockerArgs() []string {
	var args []string
	args = append(args, p.Mount.DockerArgs()...)
	if p.Network != nil {
		args = append(args, p.Network.DockerArgs()...)
	}
	for k, v := range p.EnvVars {
		args = append(args, "-e", k+"="+v)
	}
	return args
}

// InjectAuth adds Claude authentication env vars to the policy.
// Extracts the OAuth token from the macOS keychain if ANTHROPIC_API_KEY is not already set.
func (p *Policy) InjectAuth() {
	if p.EnvVars == nil {
		p.EnvVars = make(map[string]string)
	}
	// If ANTHROPIC_API_KEY already in env (e.g., from user), use it
	if _, ok := p.EnvVars["ANTHROPIC_API_KEY"]; ok {
		return
	}
	// Try to extract from keychain via helper script
	// The spawner should call this before creating the container
}
