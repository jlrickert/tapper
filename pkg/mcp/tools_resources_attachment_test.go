package mcp_test

import (
	"context"
	"encoding/base64"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func uploadAttachment(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, tool, nodeID, name string, data []byte) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: tool,
		Arguments: map[string]any{
			"node_id":     nodeID,
			"filename":    name,
			"data_base64": base64.StdEncoding.EncodeToString(data),
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%s returned error: %s", tool, extractText(t, res))
	return res
}

func resourceLinks(res *sdkmcp.CallToolResult) []*sdkmcp.ResourceLink {
	var links []*sdkmcp.ResourceLink
	for _, c := range res.Content {
		if link, ok := c.(*sdkmcp.ResourceLink); ok {
			links = append(links, link)
		}
	}
	return links
}

func TestMCP_AttachmentResource_ReadsLinkedAttachments(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: batchCreateArgs(map[string]any{"title": "Attachment Resource Node"}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	pngData := tinyPNG(t)
	upload := uploadAttachment(t, ctx, session, "upload_image", nodeID, "diagram.png", pngData)
	links := resourceLinks(upload)
	require.Len(t, links, 1)
	imageURI := "tapper://node/" + nodeID + "/attachments/image/diagram.png"
	require.Equal(t, imageURI, links[0].URI)

	csv := []byte("a,b\n1,2\n")
	uploadAttachment(t, ctx, session, "upload_file", nodeID, "my data.csv", csv)

	listRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list_files",
		Arguments: map[string]any{"node_id": nodeID},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, listRes), "my data.csv")
	links = resourceLinks(listRes)
	require.Len(t, links, 1)
	fileURI := "tapper://node/" + nodeID + "/attachments/file/my%20data.csv"
	require.Equal(t, fileURI, links[0].URI)
	require.Equal(t, "my data.csv", links[0].Name)

	image, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: imageURI})
	require.NoError(t, err)
	require.Len(t, image.Contents, 1)
	require.Equal(t, "image/png", image.Contents[0].MIMEType)
	require.Equal(t, pngData, image.Contents[0].Blob)

	file, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: fileURI})
	require.NoError(t, err)
	require.Len(t, file.Contents, 1)
	require.Equal(t, "text/csv", file.Contents[0].MIMEType)
	require.Equal(t, csv, file.Contents[0].Blob)

	// Kinds are independent namespaces: the file is not an image.
	_, err = session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "tapper://node/" + nodeID + "/attachments/image/my%20data.csv",
	})
	require.Error(t, err)

	// The node template still serves markdown and never captures attachments.
	node, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "tapper://node/" + nodeID})
	require.NoError(t, err)
	require.Equal(t, "text/markdown", node.Contents[0].MIMEType)
}

func TestMCP_AttachmentResource_RejectsInvalidURIs(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	for _, uri := range []string{
		"tapper://node/1/attachments/image/missing.png",
		"tapper://node/1/attachments/audio/song.mp3",
		"tapper://node/1/attachments/file/..",
		"tapper://node/x/attachments/file/a.txt",
	} {
		_, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: uri})
		require.Error(t, err, uri)
	}
}
