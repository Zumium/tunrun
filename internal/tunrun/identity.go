package tunrun

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

type TargetIdentity struct {
	Valid    bool
	UID      uint32
	GID      uint32
	Groups   []uint32
	Username string
	HomeDir  string
	Shell    string
}

func TargetIdentityFromEnvironment(env []string) (TargetIdentity, error) {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
		}
	}

	uidText := values["SUDO_UID"]
	gidText := values["SUDO_GID"]
	if uidText == "" && gidText == "" {
		return TargetIdentity{}, nil
	}
	if uidText == "" || gidText == "" {
		return TargetIdentity{}, fmt.Errorf("both SUDO_UID and SUDO_GID are required to run the target as the sudo caller")
	}

	uid64, err := parseID("SUDO_UID", uidText)
	if err != nil {
		return TargetIdentity{}, err
	}
	gid64, err := parseID("SUDO_GID", gidText)
	if err != nil {
		return TargetIdentity{}, err
	}

	identity := TargetIdentity{
		Valid:    true,
		UID:      uint32(uid64),
		GID:      uint32(gid64),
		Groups:   []uint32{uint32(gid64)},
		Username: values["SUDO_USER"],
	}
	identity.Fill()
	return identity, nil
}

func (id *TargetIdentity) Fill() {
	if !id.Valid {
		return
	}
	u, err := user.LookupId(strconv.FormatUint(uint64(id.UID), 10))
	if err == nil {
		if id.Username == "" {
			id.Username = shortUsername(u.Username)
		}
		if id.HomeDir == "" {
			id.HomeDir = u.HomeDir
		}
		if id.Shell == "" {
			id.Shell = lookupShell(uint64(id.UID))
		}
		if len(id.Groups) <= 1 {
			if groupIDs, groupErr := u.GroupIds(); groupErr == nil {
				id.Groups = parseGroupIDs(groupIDs, id.GID)
			}
		}
	}
	if id.Username == "" {
		id.Username = strconv.FormatUint(uint64(id.UID), 10)
	}
}

func lookupShell(uid uint64) string {
	body, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}

	uidText := strconv.FormatUint(uid, 10)
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[2] != uidText {
			continue
		}
		return fields[6]
	}
	return ""
}

func parseID(name, value string) (uint64, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return id, nil
}

func parseGroupIDs(values []string, primary uint32) []uint32 {
	groups := []uint32{primary}
	seen := map[uint32]struct{}{primary: {}}

	for _, value := range values {
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			continue
		}
		group := uint32(id)
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups
}

func shortUsername(username string) string {
	if i := strings.LastIndex(username, `\`); i >= 0 {
		return username[i+1:]
	}
	return username
}

func FormatGroupList(groups []uint32) string {
	if len(groups) == 0 {
		return ""
	}

	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		parts = append(parts, strconv.FormatUint(uint64(group), 10))
	}
	return strings.Join(parts, ",")
}

func ParseGroupList(raw string) ([]uint32, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	groups := make([]uint32, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid group id %q", part)
		}
		groups = append(groups, uint32(id))
	}
	return groups, nil
}
