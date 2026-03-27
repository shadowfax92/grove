function gv
    if test (count $argv) -eq 0
        grove
        return
    end

    set -l subcmd $argv[1]
    set -l rest $argv[2..]

    switch $subcmd
        case nd
            set -l output (grove new --cd $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case dd
            set -l output (grove done --cd $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case n new
            if contains -- --cd $rest
                set -l output (grove new $rest)
                or return $status
                set -l path (string trim -- $output[-1])
                test -n "$path"
                and cd -- $path
            else
                grove new $rest
            end
        case cd
            set -l output (grove cd $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case s sw switch
            grove switch $rest
        case ls l list
            grove list $rest
        case rm remove
            grove rm $rest
        case cfg config
            grove config $rest
        case sh shadow
            grove shadow $rest
        case '*'
            grove $argv
    end
end
