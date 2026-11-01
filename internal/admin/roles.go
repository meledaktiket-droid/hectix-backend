package admin

var AdminRoles = []string{"super_admin", "admin", "staff"}

var RoleLabels = map[string]string{
	"super_admin": "Super Admin",
	"admin":       "Admin",
	"staff":       "Staff",
}

var StaffRoles = map[string]bool{"super_admin": true, "admin": true, "staff": true}

func IsAdminRole(r string) bool { return StaffRoles[r] }

func CanManageTeam(r string) bool    { return r == "super_admin" }
func CanVerifyOrgs(r string) bool    { return r == "super_admin" || r == "admin" }
func CanManagePayouts(r string) bool { return r == "super_admin" || r == "admin" }
func CanManageContent(r string) bool { return IsAdminRole(r) }
