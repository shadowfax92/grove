function gv
    if test (count $argv) -eq 0
        grove
        return
    end

    set -l subcmd $argv[1]
    set -l rest $argv[2..]

    switch $subcmd
        case n new
            if contains -- --cd $rest
                set -l path (grove new $rest)
                and cd $path
            else
                grove new $rest
            end
        case s sw switch
            grove switch $rest
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
