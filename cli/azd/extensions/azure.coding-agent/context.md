These are two examples of how to prompt for things, from the user, in this app:

```go
// this prompts the user to select between several valules:
resp, err := prompter.Select(ctx context.Context, in *SelectRequest, opts ...grpc.CallOption) (*SelectResponse, error)

// this prompts the user to answer a yes or no question.
prompter.Confirm(ctx context.Context, in *ConfirmRequest, opts ...grpc.CallOption) (*ConfirmResponse, error)
```

I want you to generate two functions: 

1. `GitPushChanges`, a function will walk a user through commiting changes in this repo, and creating a pull request, which we can open a browser window for. This is all occurring on GitHub. 
2. `OpenBrowserToMCPConfig`, a function that will ask the user if they want to visit a specific URL (leave this as 'example', and I'll fill it in later). It should use a platform independent way to launch the browser. Look through the 'github.com/Azure/azure-dev' codebase, as they do it already.

After each pass you should run "gocheck.bat" to validate that things are working correctly.