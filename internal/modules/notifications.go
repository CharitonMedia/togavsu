package modules

import (
	"context"
	"strings"
)

// NotificationKindGroup is one enabled module's kinds for the preferences grid (CORE-03).
type NotificationKindGroup struct {
	ModuleID   string
	ModuleName string
	Kinds      []NotificationKindInfo
}

// NotificationKindInfo is one kind as the grid and createNotification see it.
type NotificationKindInfo struct {
	Kind         string // {module_id}.{short}
	Label        string
	DefaultInApp bool
	DefaultEmail bool
}

// NotificationKindGroups lists kinds for every enabled module (disable hides the group).
func (s *Service) NotificationKindGroups(ctx context.Context) ([]NotificationKindGroup, error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NotificationKindGroup, 0, len(mods))
	for _, in := range mods {
		g := NotificationKindGroup{ModuleID: in.ID, ModuleName: in.Manifest.Name}
		for _, n := range in.Manifest.Notifications {
			info := NotificationKindInfo{
				Kind:  FullNotificationKind(in.ID, n.Kind),
				Label: n.Label,
			}
			for _, c := range n.DefaultChannels {
				switch c {
				case "in_app":
					info.DefaultInApp = true
				case "email":
					info.DefaultEmail = true
				}
			}
			g.Kinds = append(g.Kinds, info)
		}
		if len(g.Kinds) > 0 {
			out = append(out, g)
		}
	}
	return out, nil
}

// DeclaresNotificationKind reports whether fullKind is one of moduleID's declared kinds.
func (s *Service) DeclaresNotificationKind(ctx context.Context, moduleID, fullKind string) (bool, error) {
	in, err := s.Installed(ctx, moduleID)
	if err != nil {
		return false, err
	}
	prefix := moduleID + "."
	if !strings.HasPrefix(fullKind, prefix) {
		return false, nil
	}
	short := strings.TrimPrefix(fullKind, prefix)
	for _, n := range in.Manifest.Notifications {
		if n.Kind == short {
			return true, nil
		}
	}
	return false, nil
}
