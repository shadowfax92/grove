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

    switch $subcmd
        case nd
            set -l output (grove new $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case nt
            grove new --tmux $rest
        case dd
            set -l output (grove done --cd $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case n new
            if contains -- --tmux $rest
                grove new $rest
            else
                set -l output (grove new $rest)
                or return $status
                set -l path (string trim -- $output[-1])
                test -n "$path"
                and cd -- $path
            end
        case cd
            set -l output (grove cd $rest)
            or return $status
            set -l path (string trim -- $output[-1])
            test -n "$path"
            and cd -- $path
        case d done
            if contains -- --cd $rest
                set -l output (grove done $rest)
                or return $status
                set -l path (string trim -- $output[-1])
                test -n "$path"
                and cd -- $path
            else
                grove done $rest
            end
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
