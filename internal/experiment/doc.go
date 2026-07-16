// Package experiment is ModelMesh's experimentation platform: it ties the Shadow
// Traffic framework and the Evaluation Engine into managed experiments and turns
// their results — together with the routing, cache, and budget subsystems'
// telemetry — into analytics reports.
//
//	Application → Intelligent Router → Primary + Shadow → Evaluation Engine
//	                                                            │
//	                                                     Analytics (BuildReport)
//	                                                            │
//	                                                     Experiment Reports
//
// # Design
//
//   - Manager owns named Experiments (create / get / list / report), safe for
//     concurrent use.
//   - Experiment bundles an evaluation.Engine (required) with optional telemetry
//     sources (shadow stats, classification distribution, cache/budget savings,
//     provider usage) and produces a Report on demand.
//   - BuildReport is a pure function over an Inputs snapshot, so analytics are
//     deterministic and testable without any live subsystem.
//
// The platform reads only already-produced telemetry; it never affects production
// traffic. Diagnostics render experiments, comparisons, evaluation history, and
// routing decisions for operators.
package experiment
