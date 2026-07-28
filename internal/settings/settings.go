// Package settings names the values the user can choose between.
//
// Every type here is a closed set of identifiers with a label: the identifier is what gets written
// to the state file and what the code branches on, the label is what Home Assistant shows. They are
// separate so that renaming what the user reads cannot silently reset what the device is doing.
//
// It holds nothing but names, and depends on nothing, so persistence does not have to import the
// subsystem a setting belongs to in order to store it, and the subsystem does not have to import
// persistence in order to be told about it.
package settings

// Labelled is a setting whose values name themselves. The entity layer binds any of these to a
// select without knowing which setting it is.
type Labelled interface{ Label() string }
