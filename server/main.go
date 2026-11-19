// Command kandev-plugin-gitea is the backend half of the Gitea plugin. Kandev
// spawns it as a gRPC subprocess; pluginsdk.Serve owns the transport.
package main

import "github.com/kdlbs/kandev/pluginsdk"

func main() {
	pluginsdk.Serve(newPlugin())
}
