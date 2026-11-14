// Package defaultimage seeds the built-in "AInsel Standard (Pi)" AgentImage,
// so a fresh install offers a ready-to-use runtime instead of asking the first
// user to copy a container URL out of the docs (issue #88).
//
// The image reference comes from the installation: the chart passes the
// release's own Pi runtime image (tag = chart appVersion), and the hub writes
// it on every start. Upgrading the release therefore upgrades the default
// image; nobody edits it by hand. The CR is marked managedBy "ainsel", which
// makes it read-only in the hub API and console — a user who needs different
// tools, env or MCP servers creates a custom image instead.
package defaultimage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Name is the AgentImage CR name. Agents reference it via imageRef.name.
	Name = "ainsel-standard-pi"
	// DisplayName is what the console shows in the image picker.
	DisplayName = "AInsel Standard (Pi)"
	// Description tells the user what the image does and does not bring.
	Description = "The maintained Pi runtime shipped with this AInsel release. " +
		"Grants no tools or MCP servers by itself; select them per agent, or create a custom image."
	// ManagedBy marks the CR as authored by the installation, not a user.
	ManagedBy = "ainsel"
)

// GroupSetter files a resource under an authz group. *authz.Store satisfies it.
type GroupSetter interface {
	SetResourceGroup(ctx context.Context, resourceType, resourceName, groupID string, public bool) error
}

// Ensure creates or upgrades the default AgentImage to point at imageURL.
//
// An empty imageURL disables seeding and leaves any existing image in place.
// An image with the same name that the installation does not manage is a
// user's, and is never overwritten. The group is assigned only when the image
// is created, so an admin's later choice of group or visibility sticks. groups
// may be nil when authz is not configured.
func Ensure(ctx context.Context, c client.Client, groups GroupSetter, namespace, imageURL string) error {
	if imageURL == "" {
		return nil
	}

	existing := &ainselv1alpha1.AgentImage{}
	err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: Name}, existing)
	switch {
	case apierrors.IsNotFound(err):
		return create(ctx, c, groups, namespace, imageURL)
	case err != nil:
		return fmt.Errorf("get default agent image: %w", err)
	}

	if existing.Spec.ManagedBy != ManagedBy {
		slog.Warn("default agent image name is taken by an image this installation does not manage; leaving it alone",
			"name", Name, "managedBy", existing.Spec.ManagedBy)
		return nil
	}
	if existing.Spec.ImageURL == imageURL &&
		existing.Spec.DisplayName == DisplayName &&
		existing.Spec.Description == Description {
		return nil
	}
	from := existing.Spec.ImageURL
	existing.Spec.ImageURL = imageURL
	existing.Spec.DisplayName = DisplayName
	existing.Spec.Description = Description
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("update default agent image: %w", err)
	}
	slog.Info("default agent image updated", "name", Name, "from", from, "to", imageURL)
	return nil
}

func create(ctx context.Context, c client.Client, groups GroupSetter, namespace, imageURL string) error {
	img := &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: Name},
		Spec: ainselv1alpha1.AgentImageSpec{
			DisplayName: DisplayName,
			Description: Description,
			ImageURL:    imageURL,
			ManagedBy:   ManagedBy,
		},
	}
	if err := c.Create(ctx, img); err != nil {
		// Another hub replica won the race; it also files the group.
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create default agent image: %w", err)
	}
	slog.Info("default agent image created", "name", Name, "image", imageURL)
	if groups == nil {
		return nil
	}
	if err := groups.SetResourceGroup(ctx, string(authz.ResourceAgentImage), Name, authz.DefaultGroupID, true); err != nil {
		return fmt.Errorf("file default agent image under group %q: %w", authz.DefaultGroupID, err)
	}
	return nil
}
