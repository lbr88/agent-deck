package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/hub"
)

type HubAdminDialog struct {
	visible           bool
	admin             bool
	width             int
	height            int
	cursor            int
	items             []hubAdminDialogItem
	creatingInvite    bool
	createInviteAdmin bool
	createFocus       int
	createNameInput   textinput.Model
	createTTLInput    textinput.Model
	validationErr     string
	lastInviteCommand string
}

type hubAdminDialogItem struct {
	kind        string
	id          string
	title       string
	description string
}

const (
	hubAdminItemTrust  = "trust"
	hubAdminItemInvite = "invite"
)

func NewHubAdminDialog() *HubAdminDialog {
	nameInput := textinput.New()
	nameInput.Placeholder = "work-laptop"
	nameInput.CharLimit = 80
	nameInput.Width = 32

	ttlInput := textinput.New()
	ttlInput.Placeholder = "24"
	ttlInput.CharLimit = 8
	ttlInput.Width = 10

	return &HubAdminDialog{
		createNameInput: nameInput,
		createTTLInput:  ttlInput,
	}
}

func (d *HubAdminDialog) SetSize(width, height int) {
	d.width = width
	d.height = height
}

func (d *HubAdminDialog) Show(admin bool, invites []hub.AdminInvite, requests []hub.TrustRequestPayload) {
	d.visible = true
	d.creatingInvite = false
	d.validationErr = ""
	d.SetAdmin(admin)
	d.cursor = 0
	d.items = d.items[:0]
	for _, req := range requests {
		nodeName := strings.TrimSpace(req.NodeName)
		if nodeName == "" {
			nodeName = req.NodeID
		}
		meta := strings.TrimSpace(strings.Join(nonEmptyStrings(req.Version, req.OS, req.Arch), " "))
		d.items = append(d.items, hubAdminDialogItem{
			kind:        hubAdminItemTrust,
			id:          req.NodeID,
			title:       fmt.Sprintf("Trust %s", nodeName),
			description: meta,
		})
	}
	if !admin {
		return
	}
	for _, invite := range invites {
		status := strings.TrimSpace(invite.Status)
		if status == "" {
			status = inviteStatus(invite)
		}
		d.items = append(d.items, hubAdminDialogItem{
			kind:        hubAdminItemInvite,
			id:          firstNonEmpty(invite.ID, invite.NodeName),
			title:       fmt.Sprintf("Invite %s", firstNonEmpty(invite.NodeName, invite.ID)),
			description: fmt.Sprintf("%s · expires %s", status, invite.ExpiresAt.Local().Format("2006-01-02 15:04")),
		})
	}
}

func (d *HubAdminDialog) Hide() {
	d.visible = false
	d.creatingInvite = false
	d.validationErr = ""
}

func (d *HubAdminDialog) IsVisible() bool {
	return d != nil && d.visible
}

func (d *HubAdminDialog) IsAdmin() bool {
	return d != nil && d.admin
}

// SetAdmin applies a live role change without closing trust management. A
// demotion strips invite rows and any one-time invite command immediately.
func (d *HubAdminDialog) SetAdmin(admin bool) {
	if d == nil {
		return
	}
	d.admin = admin
	if admin {
		return
	}
	d.creatingInvite = false
	d.validationErr = ""
	d.lastInviteCommand = ""
	items := make([]hubAdminDialogItem, 0, len(d.items))
	for _, item := range d.items {
		if item.kind == hubAdminItemTrust {
			items = append(items, item)
		}
	}
	d.items = items
	if d.cursor >= len(d.items) {
		d.cursor = max(0, len(d.items)-1)
	}
}

func (d *HubAdminDialog) Selected() (kind, id string, ok bool) {
	if d == nil || !d.visible || d.cursor < 0 || d.cursor >= len(d.items) {
		return "", "", false
	}
	item := d.items[d.cursor]
	return item.kind, item.id, true
}

func (d *HubAdminDialog) ShowCreateInvite(admin bool) {
	if d == nil || !d.admin {
		return
	}
	d.creatingInvite = true
	d.createInviteAdmin = admin
	d.createFocus = 0
	d.validationErr = ""
	d.createNameInput.SetValue("")
	d.createNameInput.Focus()
	d.createTTLInput.SetValue("24")
	d.createTTLInput.Blur()
}

func (d *HubAdminDialog) IsCreatingInvite() bool {
	return d != nil && d.visible && d.creatingInvite
}

func (d *HubAdminDialog) CreateInviteRequest() (hub.CreateAdminInviteRequest, error) {
	if d == nil || !d.admin || !d.creatingInvite {
		return hub.CreateAdminInviteRequest{}, fmt.Errorf("hub invite creation is not active")
	}
	nodeName := strings.TrimSpace(d.createNameInput.Value())
	if nodeName == "" {
		return hub.CreateAdminInviteRequest{}, fmt.Errorf("hub invite node name is required")
	}
	ttlHours, err := strconv.ParseFloat(strings.TrimSpace(d.createTTLInput.Value()), 64)
	if err != nil || math.IsNaN(ttlHours) || math.IsInf(ttlHours, 0) || ttlHours <= 0 {
		return hub.CreateAdminInviteRequest{}, fmt.Errorf("hub invite ttl hours must be greater than zero")
	}
	return hub.CreateAdminInviteRequest{
		NodeName:   nodeName,
		TTLSeconds: int64(math.Round(ttlHours * 3600)),
		Admin:      d.createInviteAdmin,
	}, nil
}

func (d *HubAdminDialog) SetValidationError(err error) {
	if d == nil {
		return
	}
	if err == nil {
		d.validationErr = ""
		return
	}
	d.validationErr = err.Error()
}

func (d *HubAdminDialog) FinishCreateInvite(resp hub.CreateAdminInviteResponse) {
	if d == nil {
		return
	}
	d.creatingInvite = false
	d.validationErr = ""
	token := strings.TrimSpace(resp.InviteToken)
	url := strings.TrimSpace(resp.URL)
	switch {
	case token != "" && url != "":
		d.lastInviteCommand = fmt.Sprintf("agent-deck hub join %s --token %s", url, token)
	case token != "":
		d.lastInviteCommand = token
	default:
		d.lastInviteCommand = ""
	}
}

func (d *HubAdminDialog) Update(msg tea.KeyMsg) (*HubAdminDialog, tea.Cmd) {
	if d == nil || !d.visible {
		return d, nil
	}
	if d.creatingInvite {
		switch msg.String() {
		case "esc":
			d.creatingInvite = false
			d.validationErr = ""
			return d, nil
		case "tab", "shift+tab":
			d.toggleCreateFocus()
			return d, nil
		case "ctrl+a":
			if !d.admin {
				return d, nil
			}
			d.createInviteAdmin = !d.createInviteAdmin
			return d, nil
		}
		var cmd tea.Cmd
		if d.createFocus == 0 {
			d.createNameInput, cmd = d.createNameInput.Update(msg)
		} else {
			d.createTTLInput, cmd = d.createTTLInput.Update(msg)
		}
		return d, cmd
	}
	switch msg.String() {
	case "esc", "q":
		d.Hide()
	case "down", "j":
		if len(d.items) > 0 {
			d.cursor = (d.cursor + 1) % len(d.items)
		}
	case "up", "k":
		if len(d.items) > 0 {
			d.cursor--
			if d.cursor < 0 {
				d.cursor = len(d.items) - 1
			}
		}
	}
	return d, nil
}

func (d *HubAdminDialog) View() string {
	if d == nil || !d.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString("Hub Management\n")
	role := "user"
	if d.admin {
		role = "admin"
	}
	b.WriteString("role: " + role + "\n\n")
	if d.admin {
		b.WriteString("c create invite · C create admin invite · Enter/a allow trust · x/d deny trust · D revoke invite · r refresh · q close\n\n")
	} else {
		b.WriteString("Enter/a allow trust · x/d deny trust · r refresh · q close\n\n")
	}
	if d.lastInviteCommand != "" {
		b.WriteString("Last invite: ")
		b.WriteString(d.lastInviteCommand)
		b.WriteString("\n\n")
	}
	if d.creatingInvite {
		role := "user"
		if d.createInviteAdmin {
			role = "admin"
		}
		b.WriteString(fmt.Sprintf("Create %s invite (Tab switches fields, Ctrl+A toggles admin, Enter creates, Esc cancels)\n", role))
		if d.validationErr != "" {
			b.WriteString("Error: ")
			b.WriteString(d.validationErr)
			b.WriteByte('\n')
		}
		b.WriteString("Node name: ")
		b.WriteString(d.createNameInput.View())
		b.WriteByte('\n')
		b.WriteString("TTL hours: ")
		b.WriteString(d.createTTLInput.View())
		b.WriteString("\n\n")
	}
	if len(d.items) == 0 {
		b.WriteString("No pending trust requests or active invites.\n")
		return b.String()
	}
	for i, item := range d.items {
		prefix := "  "
		if i == d.cursor {
			prefix = "> "
		}
		b.WriteString(prefix)
		b.WriteString(item.title)
		if item.description != "" {
			b.WriteString(" — ")
			b.WriteString(item.description)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (d *HubAdminDialog) toggleCreateFocus() {
	if d == nil {
		return
	}
	if d.createFocus == 0 {
		d.createFocus = 1
		d.createNameInput.Blur()
		d.createTTLInput.Focus()
		return
	}
	d.createFocus = 0
	d.createTTLInput.Blur()
	d.createNameInput.Focus()
}

func inviteStatus(invite hub.AdminInvite) string {
	now := time.Now()
	switch {
	case invite.RevokedAt != nil:
		return "revoked"
	case invite.ConsumedAt != nil:
		return "consumed"
	case !invite.ExpiresAt.IsZero() && invite.ExpiresAt.Before(now):
		return "expired"
	default:
		return "active"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
