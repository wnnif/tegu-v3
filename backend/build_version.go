package main

// Release builds inject these with -ldflags "-X main.Version=3.0
// -X main.BuildCommit=<commit>". Source builds remain visibly unversioned.
var Version = "development"
var BuildCommit = "development"
