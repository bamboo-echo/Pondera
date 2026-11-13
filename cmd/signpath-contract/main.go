// Command signpath-contract validates the checked-in SignPath signing policy
// contract and prints its fingerprint. The contract is the single source of
// truth for the provider allowlist and every workflow that can reach signing.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const contractPath = ".signpath/contracts/release-signing.yml"

type contract struct {
	Version                 int      `yaml:"version"`
	ProjectSlug             string   `yaml:"project_slug"`
	SigningPolicySlug       string   `yaml:"signing_policy_slug"`
	RepositoryURL           string   `yaml:"repository_url"`
	AllowedBranchNames      []string `yaml:"allowed_branch_names"`
	AllowedBuildDefinitions []string `yaml:"allowed_build_definitions"`
	FingerprintFiles        []string `yaml:"fingerprint_files"`
}

func main() {
	if len(os.Args) != 2 {
		fail("usage: signpath-contract <validate|fingerprint>")
	}
	root, err := repositoryRoot()
	if err != nil {
		fail(err.Error())
	}
	doc, err := load(root)
	if err != nil {
		fail(err.Error())
	}
	switch os.Args[1] {
	case "validate":
		if err := validate(root, doc); err != nil {
			fail(err.Error())
		}
		fmt.Println("signpath contract: valid")
	case "fingerprint":
		sum, err := fingerprint(root, doc)
		if err != nil {
			fail(err.Error())
		}
		fmt.Println(sum)
	default:
		fail("usage: signpath-contract <validate|fingerprint>")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, contractPath)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s not found", contractPath)
		}
		dir = parent
	}
}

func load(root string) (contract, error) {
	raw, err := os.ReadFile(filepath.Join(root, contractPath))
	if err != nil {
		return contract{}, err
	}
	var doc contract
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return contract{}, fmt.Errorf("parse %s: %w", contractPath, err)
	}
	return doc, nil
}

func validate(root string, doc contract) error {
	if doc.Version != 1 {
		return fmt.Errorf("contract version must be 1, got %d", doc.Version)
	}
	for name, value := range map[string]string{
		"project_slug": doc.ProjectSlug, "signing_policy_slug": doc.SigningPolicySlug, "repository_url": doc.RepositoryURL,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("contract is missing %s", name)
		}
	}
	if !contains(doc.AllowedBranchNames, "main-v2") {
		return fmt.Errorf("allowed_branch_names must include main-v2")
	}
	if len(doc.AllowedBuildDefinitions) == 0 {
		return fmt.Errorf("allowed_build_definitions is empty")
	}
	for _, path := range append(append([]string{}, doc.AllowedBuildDefinitions...), doc.FingerprintFiles...) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return fmt.Errorf("contract references a missing file: %s", path)
		}
	}
	// No top-level workflow may reach signing unless the contract lists it.
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		return err
	}
	for _, path := range workflows {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(raw)
		if !strings.Contains(body, "setup-certum") && !strings.Contains(body, "signpath") && !strings.Contains(body, "SignPath") {
			continue
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if !contains(doc.AllowedBuildDefinitions, relative) {
			return fmt.Errorf("workflow reaches signing but is not an allowed build definition: %s", relative)
		}
	}
	return nil
}

func fingerprint(root string, doc contract) (string, error) {
	if len(doc.FingerprintFiles) == 0 {
		return "", fmt.Errorf("fingerprint_files is empty")
	}
	digest := sha256.New()
	for _, path := range doc.FingerprintFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", fmt.Errorf("read fingerprint file %s: %w", path, err)
		}
		fmt.Fprintf(digest, "%s\x00", path)
		digest.Write(raw)
		digest.Write([]byte{0})
	}
	return "v1:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
