package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func main() {
	latest := flag.Bool("latest", false, "pre-fill tag with the latest reachable tag from current commit")
	flag.BoolVar(latest, "l", false, "pre-fill tag with the latest reachable tag from current commit (shorthand)")
	flag.Parse()

	repo, err := getRepoInfo()
	if err != nil {
		fmt.Println("error getting repo information", err)
		os.Exit(1)
	}

	initialTag := "v"
	if *latest {
		tag, err := getLatestReachableTag(repo.Repository)
		if err != nil {
			fmt.Println("error finding latest reachable tag:", err)
			os.Exit(1)
		}
		initialTag = tag
	}

	resultCommandChan := make(chan string, 1)
	if err := tea.NewProgram(initialModel(repo.currentBranch, initialTag, resultCommandChan)).Start(); err != nil {
		fmt.Printf("could not start program: %s\n", err)
		os.Exit(1)
	}

	select {
	case result := <-resultCommandChan:
		if err := executeCommand(result); err != nil {
			fmt.Println("error executing command: ", err.Error())
			os.Exit(1)
		}
	default:
		os.Exit(0)
	}
}

func executeCommand(cmdStr string) error {
	fmt.Println("Executing command:", cmdStr)
	cmd := exec.Command("bash", "-c", cmdStr)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

var (
	focusedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	cursorStyle  = focusedStyle.Copy()
	noStyle      = lipgloss.NewStyle()
	lightStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Copy()
	boldStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	focusedButton = focusedStyle.Copy().Render("[ Tag and Push ]")
	blurredButton = fmt.Sprintf("[ %s ]", lightStyle.Render("Tag and Push"))
)

type model struct {
	focusIndex        int
	inputs            []textinput.Model
	cursorMode        textinput.CursorMode
	tagCommandOutChan chan string
}

func initialModel(branch string, initialTag string, tagCommand chan string) model {
	m := model{
		inputs:            make([]textinput.Model, 2),
		tagCommandOutChan: tagCommand,
	}

	var t textinput.Model
	for i := range m.inputs {
		t = textinput.NewModel()
		t.CursorStyle = cursorStyle

		switch i {
		case 0:
			t.Focus()
			t.Placeholder = "tag"
			t.SetValue(initialTag)
			t.CharLimit = 64
			t.PromptStyle = focusedStyle
			t.TextStyle = focusedStyle
		case 1:
			t.Placeholder = "branch"
			t.CharLimit = 200
			t.SetValue(branch)
		}

		m.inputs[i] = t
	}

	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		// Change cursor mode
		case "ctrl+r":
			m.cursorMode++
			if m.cursorMode > textinput.CursorHide {
				m.cursorMode = textinput.CursorBlink
			}
			cmds := make([]tea.Cmd, len(m.inputs))
			for i := range m.inputs {
				cmds[i] = m.inputs[i].SetCursorMode(m.cursorMode)
			}
			return m, tea.Batch(cmds...)

		// Set focus to next input
		case "tab", "shift+tab", "enter", "up", "down":
			s := msg.String()

			// Did the user press enter while the submit button was focused?
			// If so, exit.
			if s == "enter" && m.focusIndex == len(m.inputs) {
				m.tagCommandOutChan <- strings.Join(m.command(), "")
				return m, tea.Batch(tea.ExitAltScreen, tea.Quit)
			}

			// Cycle indexes
			if s == "up" || s == "shift+tab" {
				m.focusIndex--
			} else {
				m.focusIndex++
			}

			if m.focusIndex > len(m.inputs) {
				m.focusIndex = 0
			} else if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs)
			}

			cmds := make([]tea.Cmd, len(m.inputs))
			for i := 0; i <= len(m.inputs)-1; i++ {
				if i == m.focusIndex {
					// Set focused state
					cmds[i] = m.inputs[i].Focus()
					m.inputs[i].PromptStyle = focusedStyle
					m.inputs[i].TextStyle = focusedStyle
					continue
				}
				// Remove focused state
				m.inputs[i].Blur()
				m.inputs[i].PromptStyle = noStyle
				m.inputs[i].TextStyle = noStyle
			}

			return m, tea.Batch(cmds...)
		}
	}

	// Handle character input and blinking
	cmd := m.updateInputs(msg)

	return m, cmd
}

func (m *model) updateInputs(msg tea.Msg) tea.Cmd {
	var cmds = make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m model) View() string {
	var b strings.Builder

	for i := range m.inputs {
		b.WriteString(m.inputs[i].View())
		if i < len(m.inputs)-1 {
			b.WriteRune('\n')
		}
	}

	button := &blurredButton
	if m.focusIndex == len(m.inputs) {
		button = &focusedButton
	}
	fmt.Fprintf(&b, "\n\n%s\n\n", *button)

	formatLightBold(&b, m.command()...)
	return b.String()
}

func (m model) command() []string {
	tag := m.inputs[0].Value()
	branch := m.inputs[1].Value()
	command := []string{
		`git tag -a `, tag, ` -m "source=manual,branch=`, branch, `,tag=`, tag,
		`" && git push origin `, tag,
	}
	return command
}

func formatLightBold(b *strings.Builder, s ...string) {
	for i := range s {
		if i%2 == 0 {
			b.WriteString(lightStyle.Render(s[i]))
		} else {
			b.WriteString(boldStyle.Render(s[i]))
		}
	}
}

type repoInfo struct {
	*git.Repository
	currentBranch string
}

func getRepoInfo() (*repoInfo, error) {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return nil, err
	}

	branchRefs, err := repo.Branches()
	if err != nil {
		return nil, err
	}

	headRef, err := repo.Head()
	if err != nil {
		return nil, err
	}

	var currentBranch string
	_ = branchRefs.ForEach(func(bf *plumbing.Reference) error {
		if bf.Hash() == headRef.Hash() {
			currentBranch = bf.Name().Short()
			return nil
		}
		return nil
	})

	return &repoInfo{repo, currentBranch}, nil
}

// getLatestReachableTag walks commit history from HEAD and returns the name of
// the closest ancestor tag, mirroring `git describe --tags --abbrev=0`.
func getLatestReachableTag(repo *git.Repository) (string, error) {
	headRef, err := repo.Head()
	if err != nil {
		return "", err
	}

	// Build a map of commit hash -> tag name from all tags.
	tagMap := map[plumbing.Hash]string{}
	tags, err := repo.Tags()
	if err != nil {
		return "", err
	}
	_ = tags.ForEach(func(ref *plumbing.Reference) error {
		// Annotated tag: resolve the tag object to its target commit.
		if tagObj, err := repo.TagObject(ref.Hash()); err == nil {
			tagMap[tagObj.Target] = ref.Name().Short()
		} else {
			// Lightweight tag: points directly to a commit.
			tagMap[ref.Hash()] = ref.Name().Short()
		}
		return nil
	})

	// Walk commits from HEAD; stop at the first tagged commit.
	logIter, err := repo.Log(&git.LogOptions{From: headRef.Hash()})
	if err != nil {
		return "", err
	}

	var found string
	errStop := fmt.Errorf("stop")
	_ = logIter.ForEach(func(c *object.Commit) error {
		if tag, ok := tagMap[c.Hash]; ok {
			found = tag
			return errStop
		}
		return nil
	})

	if found == "" {
		return "", fmt.Errorf("no reachable tags found")
	}
	return found, nil
}
