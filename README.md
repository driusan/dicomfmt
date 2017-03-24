# dicomfmt

dicomfmt is a tool for helping organize dicom files into a consistent
hierarchy on the filesystem.

dicomfmt has two modes of operation: copying files from multiple folders
into a directory while organizing them, or re-organizing any files that were
moved in an already "organized" folder by moving them. The former happens
when you specify multiple directories on the command line (treating the last
as the target directory to format them into), and the latter if only one
parameter is supplied (used as both the source and target directory.)

Each series will be organized into the format:

    targetDir/PatientName/SeriesName/[*].dcm

The name of any series directories that were created will be printed to
STDOUT.
