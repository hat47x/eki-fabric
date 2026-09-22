module eki.local/learned-scheduling-residual-simulation

go 1.23

require (
    eki.local/artifact-locality-spike v0.0.0
    eki.local/learned-scheduling-shadow-observation v0.0.0
)

replace eki.local/artifact-locality-spike => ../artifact-locality-cost
replace eki.local/learned-scheduling-shadow-observation => ../learned-scheduling-shadow-observation
