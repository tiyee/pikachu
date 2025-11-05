package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	fmt.Println("🧪 Running Pikachu Test Suite")
	fmt.Println("================================")

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current directory: %v\n", err)
		os.Exit(1)
	}

	// Change to project root if we're in tests directory
	if filepath.Base(cwd) == "tests" {
		if err := os.Chdir(".."); err != nil {
			fmt.Printf("Error changing to parent directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Run tests with different options
	testCommands := []struct {
		name string
		args []string
	}{
		{
			name: "Unit Tests",
			args: []string{"test", "./tests/", "-v"},
		},
		{
			name: "Race Condition Tests",
			args: []string{"test", "./tests/", "-race", "-v"},
		},
		{
			name: "Coverage Report",
			args: []string{"test", "./tests/", "-cover", "-coverprofile=coverage.out", "-v"},
		},
		{
			name: "Benchmark Tests",
			args: []string{"test", "./tests/", "-bench=.", "-benchmem", "-v"},
		},
	}

	// Create results directory
	if err := os.MkdirAll("test-results", 0755); err != nil {
		fmt.Printf("Error creating test-results directory: %v\n", err)
	}

	for i, cmd := range testCommands {
		fmt.Printf("\n📋 Running %s...\n", cmd.name)
		fmt.Println(strings.Repeat("-", 50))

		testCmd := exec.Command("go", cmd.args...)
		testCmd.Stdout = os.Stdout
		testCmd.Stderr = os.Stderr

		if err := testCmd.Run(); err != nil {
			fmt.Printf("❌ %s failed: %v\n", cmd.name, err)

			// Ask user if they want to continue
			if i < len(testCommands)-1 {
				fmt.Print("\nContinue with remaining tests? (y/n): ")
				var response string
				fmt.Scanln(&response)
				if response != "y" && response != "Y" {
					fmt.Println("Test suite aborted by user.")
					os.Exit(1)
				}
			}
		} else {
			fmt.Printf("✅ %s passed\n", cmd.name)
		}
	}

	fmt.Println("\n🎉 Test suite completed!")
	fmt.Println("================================")

	// Check if coverage report was generated
	if _, err := os.Stat("coverage.out"); err == nil {
		fmt.Println("📊 Coverage report generated: coverage.out")
		fmt.Println("Run 'go tool cover -html=coverage.out' to view HTML report")
	}
}
