// Package tasksuggest turns the commands an agent ran, as package observe
// recorded them, into Runefile tasks a person can review and apply (spec
// 058): it joins the events of each call, groups repeated commands, leaves
// out what is already a task, and writes the task text.
//
// It reads records and Runefile text only. No recorded command is run to
// group, rank, preview or apply a suggestion.
package tasksuggest
