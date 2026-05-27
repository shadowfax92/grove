function gv
    if test (count $argv) -eq 0
        set -l output (grove cd)
        or return $status
        set -l path (string trim -- $output[-1])
        test -n "$path"
        and cd -- $path
        return
    end

    set -l subcmd $argv[1]
    set -l rest $argv[2..]

    if contains -- --help $rest; or contains -- -h $rest
        grove $argv
        return
    end

    switch $subcmd
        case n nd new
            set -l output (grove new $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case cd
            set -l output (grove cd $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case d dd done
            set -l output (grove done $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case ls l list
            grove list $rest
        case rm remove
            grove rm $rest
        case cfg config
            grove config $rest
        case '*'
            grove $argv
    end
end
