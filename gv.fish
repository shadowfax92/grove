function gv
    if test (count $argv) -eq 0
        set -l path (grove)
        or return $status
        test -n "$path"
        and cd -- $path
        return
    end

    set -l subcommand $argv[1]
    set -l rest $argv[2..]

    if contains -- --help $rest; or contains -- -h $rest; or contains -- --json $rest
        grove $argv
        return $status
    end

    switch $subcommand
        case n new
            set -l path (grove new $rest)
            or return $status
            test -n "$path"
            and cd -- $path
        case cd
            set -l path (grove cd $rest)
            or return $status
            test -n "$path"
            and cd -- $path
        case rm
            if contains -- --merged $rest; or contains -- --dry-run $rest
                grove rm $rest
                return $status
            end
            set -l path (grove rm $rest)
            or return $status
            test -n "$path"
            and cd -- $path
        case l ls list
            grove list $rest
        case cfg config
            grove config $rest
        case '*'
            grove $argv
    end
end
