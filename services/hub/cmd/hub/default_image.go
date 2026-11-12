package main

import (
	"context"
	"fmt"

	"github.com/ainsel-project/ainsel/services/hub/internal/defaultimage"
)

// runDefaultImageSeed writes the built-in AgentImage once the cache is in
// sync. The authz store is optional: without it the image is created but
// filed under no group, which is how every resource behaves in dev mode.
func runDefaultImageSeed(ctx context.Context, c *container, imageURL string) error {
	if imageURL == "" {
		return nil
	}
	if !c.mgr.GetCache().WaitForCacheSync(ctx) {
		return fmt.Errorf("cache sync failed, skipping default agent image")
	}
	var groups defaultimage.GroupSetter
	if c.authzStore != nil {
		groups = c.authzStore
	}
	return defaultimage.Ensure(ctx, c.apiClient, groups, c.namespace, imageURL)
}
