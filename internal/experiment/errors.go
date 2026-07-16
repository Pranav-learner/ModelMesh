package experiment

import "errors"

// Sentinel errors for the experiment platform. Callers match with errors.Is.
var (
	// ErrExperimentExists indicates creating an experiment whose name is taken.
	ErrExperimentExists = errors.New("experiment already exists")
	// ErrExperimentNotFound indicates an operation referenced an unknown experiment.
	ErrExperimentNotFound = errors.New("experiment not found")
	// ErrInvalidExperiment indicates a missing name or nil evaluation engine.
	ErrInvalidExperiment = errors.New("invalid experiment")
)
