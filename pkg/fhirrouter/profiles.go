package fhirrouter

import (
	"context"
	"errors"

	"trivita-fhir/pkg/fhirvalidate"
)

// validateOptions dựng tùy chọn của fhirvalidate cho một yêu cầu. Khi ValidateProfiles bật, resource có meta.profile được kiểm
// theo StructureDefinition tương ứng trong kho (cùng phạm vi project với người gọi, qua repoOf), xem fhirvalidate/profile.go.
func (r *Router) validateOptions(ctx context.Context) *fhirvalidate.Options {
	o := &fhirvalidate.Options{SkipConstraints: r.opts.SkipConstraints}
	if r.opts.ValidateProfiles {
		o.Profiles = r.profileResolver(ctx)
		if r.opts.ProfileUnknownError {
			o.UnknownProfile = fhirvalidate.Error
		}
	}
	return o
}

// profileResolver tra StructureDefinition theo canonical bằng kho; không có thì (nil, nil).
func (r *Router) profileResolver(ctx context.Context) fhirvalidate.ProfileResolver {
	repo := r.repoOf(ctx)
	return func(canonical string) (map[string]any, error) {
		res, err := r.findByURL(ctx, repo, "StructureDefinition", canonical)
		if err != nil {
			var e *Error
			if errors.As(err, &e) && e.Status == 404 {
				return nil, nil
			}
			return nil, err
		}
		return toMap(res)
	}
}
