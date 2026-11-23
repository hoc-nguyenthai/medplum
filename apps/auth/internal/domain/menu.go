package domain

import "trivita-fhir/pkg/fhir"

func link(name, target string) fhir.UserConfigurationMenuLink {
	return fhir.UserConfigurationMenuLink{Name: name, Target: target}
}

// DefaultMenu dựng menu mặc định của UserConfiguration (auth/me.ts:166): Favorites cho mọi người; Admin cho admin của project;
// Super Admin cho project super admin.
func DefaultMenu(project *fhir.Project, membership *fhir.ProjectMembership) []fhir.UserConfigurationMenu {
	var fav []fhir.UserConfigurationMenuLink
	for _, rt := range []string{"Patient", "Practitioner", "Organization", "ServiceRequest", "DiagnosticReport", "Questionnaire"} {
		fav = append(fav, link(rt, "/"+rt))
	}
	menu := []fhir.UserConfigurationMenu{{Title: "Favorites", Link: fav}}
	super := project != nil && IsTrue(project.SuperAdmin)
	if membership != nil && IsTrue(membership.Admin) {
		admin := []fhir.UserConfigurationMenuLink{link("Project", "/admin/project"), link("AccessPolicy", "/AccessPolicy"), link("Subscriptions", "/Subscription"), link("Batch", "/batch")}
		if !super {
			admin = append(admin, link("Config", "/admin/config"))
		}
		menu = append(menu, fhir.UserConfigurationMenu{Title: "Admin", Link: admin})
	}
	if super {
		menu = append(menu, fhir.UserConfigurationMenu{Title: "Super Admin", Link: []fhir.UserConfigurationMenuLink{
			link("Projects", "/Project"), link("Super Config", "/admin/super"), link("Super AsyncJob", "/admin/super/asyncjob"), link("Super DB", "/admin/super/db"),
		}})
	}
	return menu
}
