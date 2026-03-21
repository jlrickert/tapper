package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerLicenseTools(srv *sdkmcp.Server, licenseText string) {
	registerLicense(srv, licenseText)
}

// --- license ---

type licenseInput struct{}

func registerLicense(srv *sdkmcp.Server, licenseText string) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "license",
		Description: "Return the full license text embedded in the binary",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in licenseInput) (*sdkmcp.CallToolResult, any, error) {
		if licenseText == "" {
			return textResult("no license text available"), nil, nil
		}
		return textResult(licenseText), nil, nil
	})
}
